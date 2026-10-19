package party.openwatch

import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockEngineConfig
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respondError
import io.ktor.client.request.HttpRequestData
import io.ktor.client.request.HttpResponseData
import io.ktor.http.HttpStatusCode
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.channels.ReceiveChannel
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonObject
import kotlin.random.Random
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertSame
import kotlin.test.assertTrue

/** A whole party against a scripted relay, in virtual time: language rules and the cases the party traces don't pin. */
@OptIn(ExperimentalCoroutinesApi::class)
class PartyTest {
    /**
     * One WebSocket as the relay sees it. Pings get a pong at once. Every message the client sends must be free of
     * repeated member names; sending one of type [throwOn] throws.
     */
    private class FakeSocket(
        val url: String,
        private val now: () -> Long,
        private val throwOn: String?,
    ) : Socket {
        private val toClient = Channel<String>(Channel.UNLIMITED)
        val sent = Channel<JsonObject>(Channel.UNLIMITED)
        private var code: Int? = null
        var closedByClient: Int? = null
            private set

        override val incoming: ReceiveChannel<String> get() = toClient

        override suspend fun closeCode(): Int? = code

        override fun send(text: String) {
            assertNoRepeatedNames(text)
            val message = Json.parseToJsonElement(text).asObject()!!
            if (message["type"].asString() == throwOn) throw IllegalStateException("socket failed sending $throwOn")
            if (message["type"].asString() != "ping") {
                sent.trySend(message)
                return
            }
            reply(buildJsonObject {
                put("type", "pong")
                put("id", message["id"]!!)
                put("serverTime", now() + 1_000_000)
            })
        }

        override fun close(code: Int) {
            closedByClient = code
            toClient.close()
        }

        fun reply(message: JsonObject) {
            toClient.trySend(message.toString())
        }

        /** The relay drops the connection, with [code] or without a close status. */
        fun drop(code: Int?) {
            this.code = code
            toClient.close()
        }

        /** The next message of [type] the client sent, skipping the others (`ping`, `status`, …). */
        suspend fun next(type: String): JsonObject {
            while (true) {
                val message = sent.receive()
                if (message["type"].asString() == type) return message
            }
        }

        /** The types of the messages the client sent since the last call, pings excluded. */
        fun drain(): List<String> = generateSequence { sent.tryReceive().getOrNull() }.map { it["type"].asString()!! }.toList()
    }

    /** Opens a [FakeSocket] per connection. The next socket opened throws when the client sends a message of type [throwOnSend]. */
    private class FakeRelay(private val now: () -> Long) : Transport {
        val sockets = Channel<FakeSocket>(Channel.UNLIMITED)
        var throwOnSend: String? = null
        var opened = 0
            private set

        override suspend fun open(url: String): Socket {
            opened++
            val socket = FakeSocket(url, now, throwOnSend)
            throwOnSend = null
            return socket.also { sockets.trySend(it) }
        }
    }

    private fun roomMessage(rev: Long, vararg peers: String) = JsonObject(room(rev, *peers) + ("type" to JsonPrimitive("room")))

    private fun room(rev: Long, vararg peers: String) = buildJsonObject {
        put("rev", rev)
        put("code", "K7MQ2X")
        put("epoch", 0)
        put("phase", "idle")
        put("positionMs", 0)
        put("atMs", 0)
        put("rate", 1.0)
        put("control", "host")
        put("locked", false)
        put("waitingOn", JsonArray(emptyList()))
        put(
            "participants",
            JsonArray(
                peers.map { peer ->
                    buildJsonObject {
                        put("peerId", peer)
                        put("name", peer)
                        put("client", "Test/1")
                        put("host", peer == peers[0])
                        put("connected", true)
                        put("player", "idle")
                        put("match", JsonNull)
                        put("offsetMs", 0)
                    }
                },
            ),
        )
    }

    /** `joined` for p2; p1 is the host unless [host]. */
    private fun joined(rev: Long, host: Boolean = false) = buildJsonObject {
        put("type", "joined")
        put("code", "K7MQ2X")
        putJsonObject("you") {
            put("peerId", "p2")
            put("token", TOKEN)
        }
        put("room", if (host) room(rev, "p2", "p1") else room(rev, "p1", "p2"))
        put("media", JsonNull)
        put("serverTime", 0)
    }

    private fun error(code: String, re: String) = buildJsonObject {
        put("type", "error")
        put("code", code)
        put("message", code)
        put("re", re)
    }

    private class Fixture(val party: WatchParty<String>, val relay: FakeRelay, val events: List<PartyEvent>)

    private class Session(val party: WatchParty<String>, val relay: FakeRelay, val socket: FakeSocket, val events: List<PartyEvent>)

    /** The public list, whose background refresh gets HTTP 404. */
    private fun notFoundRelays(): Relays = Relays.public(HttpClient(MockEngine { respondError(HttpStatusCode.NotFound) }))

    /** A party on [FakeRelay] with its events recorded. */
    private fun TestScope.fixture(
        relays: Relays = notFoundRelays(),
        resolver: Resolver<String> = Resolver { emptyList() },
    ): Fixture {
        val relay = FakeRelay { testScheduler.currentTime }
        val party = WatchParty(relays, "Test/1.0", "Ben", resolver, backgroundScope, { testScheduler.currentTime }, Random(1), relay)
        val events = mutableListOf<PartyEvent>()
        backgroundScope.launch(start = CoroutineStart.UNDISPATCHED) { party.events.collect { events += it } }
        return Fixture(party, relay, events)
    }

    /** A party that joined K7MQ2X, as its host when [host]. */
    private suspend fun TestScope.joinedSession(
        host: Boolean = false,
        resolver: Resolver<String> = Resolver { emptyList() },
        relays: Relays = notFoundRelays(),
    ): Session {
        val f = fixture(relays = relays, resolver = resolver)
        val joining = async { f.party.join("k7m-q2x") }
        val socket = f.relay.sockets.receive()
        assertEquals("K7MQ2X", socket.next("join")["code"].asString())
        socket.reply(joined(rev = 1, host = host))
        joining.await()
        assertEquals(Connection.CONNECTED, f.party.state.value.connection)
        return Session(f.party, f.relay, socket, f.events)
    }

    @Test
    fun setMediaRefusesUnpairedSurrogates() = runTest {
        val s = joinedSession(host = true)
        s.socket.next("status")
        val broken = Media(MediaKind.MOVIE, "Heat\uD83D", durationMs = 600_000)
        assertEquals(PartyException.INVALID_MESSAGE, assertFailsWith<PartyException> { s.party.setMedia(broken, "heat.mkv") }.code)
        s.party.setMedia(broken.copy(title = "Heat \uD83D\uDE00"), "heat.mkv")
        assertEquals("Heat \uD83D\uDE00", s.socket.next("setMedia")["media"].asObject()?.get("title").asString())
    }

    @Test
    fun anExceptionInAResumeAttemptIsAFailedAttempt() = runTest {
        val s = joinedSession()
        s.relay.throwOnSend = "resume"
        s.socket.drop(1001)
        val second = s.relay.sockets.receive()
        val third = s.relay.sockets.receive()
        third.next("resume")
        assertEquals(1000, second.closedByClient)
        third.reply(joined(rev = 2))
        third.next("status")
        assertEquals(Connection.CONNECTED, s.party.state.value.connection)
    }

    @Test
    fun aSlowSubscriberGetsEveryEvent() = runTest {
        val s = joinedSession()
        val reading = CompletableDeferred<Unit>()
        val slow = mutableListOf<PartyEvent>()
        backgroundScope.launch(start = CoroutineStart.UNDISPATCHED) {
            s.party.events.collect {
                reading.await()
                slow += it
            }
        }
        // 200 participants join, one snapshot each, while the subscriber is stuck on the first event.
        val peers = mutableListOf("p1", "p2")
        for (rev in 2L..201L) {
            peers += "p${rev + 1}"
            s.socket.reply(roomMessage(rev, *peers.toTypedArray()))
        }
        runCurrent()
        assertEquals(202, s.party.state.value.room?.participants?.size)
        reading.complete(Unit)
        runCurrent()
        assertEquals((3..202).map { "p$it" }, slow.map { assertIs<PartyEvent.ParticipantJoined>(it).participant.peerId })
    }

    // -- Matching, the player and local commands (SDK.md §9.5, §9.6, §11, §12, §13) --------------------------------

    private val movie = Media(MediaKind.MOVIE, "Heat", durationMs = 600_000)

    /**
     * A `room` of [epoch] in [phase]; p2 (this party) hosts when [host]. [atMs] is relay time ([relayNow]). With
     * [media], the snapshot starts its epoch and carries that descriptor (SDK.md §8).
     */
    private fun snapshot(
        rev: Long,
        phase: String,
        host: Boolean = true,
        positionMs: Long = 0,
        atMs: Long = 0,
        rate: Double = 1.0,
        epoch: Long = 1,
        media: JsonElement? = null,
    ): JsonObject {
        val peers = if (host) arrayOf("p2", "p1") else arrayOf("p1", "p2")
        val fields = mapOf(
            "epoch" to JsonPrimitive(epoch),
            "phase" to JsonPrimitive(phase),
            "positionMs" to JsonPrimitive(positionMs),
            "atMs" to JsonPrimitive(atMs),
            "rate" to JsonPrimitive(rate),
        )
        return JsonObject(roomMessage(rev, *peers) + fields + listOfNotNull(media?.let { "media" to it }))
    }

    /** The `room` (rev [rev]) that starts [epoch] with [media], paused at 0. */
    private fun newEpoch(rev: Long, epoch: Long, media: Media, host: Boolean = false): JsonObject =
        snapshot(rev, "paused", host, epoch = epoch, media = media.toJson())

    /** Relay time: the fake relay answers pings at once with local time + 1 000 000. */
    private fun TestScope.relayNow(): Long = testScheduler.currentTime + 1_000_000

    /**
     * A player that stands still and logs every command; while [held] is set, each command waits for it, and while
     * [refusal] is set, `play` throws it and the player stays paused.
     */
    private class TestPlayer : PlayerAdapter {
        override var positionMs = 0L
        override val durationMs: Long = 600_000
        override var paused = true
        override val buffering = false
        override val firstFrameShown = true
        override val canSetRate = true
        val log = mutableListOf<String>()
        var held: CompletableDeferred<Unit>? = null
        var refusal: Exception? = null

        private suspend fun run(command: String) {
            log += command
            held?.await()
        }

        override suspend fun play() {
            run("play")
            refusal?.let { throw it }
            paused = false
        }

        override suspend fun pause() {
            run("pause")
            paused = true
        }

        override suspend fun seek(positionMs: Long) {
            run("seek $positionMs")
            this.positionMs = positionMs
        }

        override suspend fun setRate(rate: Double) = run("setRate $rate")
    }

    /** The host shared [movie] as heat.mkv (epoch 1, the room paused at 0) and [player] is bound and ready. */
    private suspend fun TestScope.boundSession(player: TestPlayer): Session {
        val s = joinedSession(host = true)
        s.party.setMedia(movie, "heat.mkv")
        s.socket.next("setMedia")
        s.socket.reply(newEpoch(rev = 2, epoch = 1, movie, host = true))
        runCurrent()
        s.party.attachPlayer(player, "heat.mkv")
        runCurrent()
        assertEquals(listOf("setRate 1.0"), player.log)
        assertEquals(PlayerState.READY, s.party.state.value.player)
        return s
    }

    /** [state] holds the party's own [media]: its choice is the file passed to `setMedia`, matched (SDK.md §9.1). */
    private fun assertOwnMedia(state: PartyState<String>, media: Media = movie) {
        val choice = assertNotNull(state.choice, "choice")
        assertEquals("", choice.id)
        assertEquals("heat.mkv", choice.ref)
        assertEquals(media.durationMs, choice.durationMs)
        assertEquals(listOf(null, null, null, null), listOf(choice.sizeBytes, choice.oshash, choice.name, choice.probe))
        assertEquals(Match(MatchState.MATCHED, 0), state.match)
        assertEquals(null, state.mismatch)
        assertEquals(emptyList(), state.candidates)
        assertEquals(0, state.offsetMs)
    }

    @Test
    fun theOwnChoiceCarriesTheDescriptorsDurationAndNoFileDetails() = runTest {
        val s = joinedSession(host = true)
        val shared = movie.copy(file = MediaFile(sizeBytes = 4_700_000_000, oshash = "8e245d9679d31e12", name = "Heat.mkv"))
        s.party.setMedia(shared, "heat.mkv")
        s.socket.next("setMedia")
        s.socket.reply(newEpoch(rev = 2, epoch = 1, shared, host = true))
        runCurrent()
        val choice = assertNotNull(s.party.state.value.choice)
        assertEquals(shared.durationMs, choice.durationMs)
        assertEquals(listOf(null, null, null, null), listOf(choice.sizeBytes, choice.oshash, choice.name, choice.probe))
    }

    @Test
    fun aProbeThatThrowsACancellationOfItsOwnReturnedNothingAndTheWalkGoesOn() = runTest {
        val probes = listOf(
            Candidate("a", "a", probe = { withTimeout(1_000) { awaitCancellation() } }),
            Candidate("b", "b", probe = { throw CancellationException("the probe gave up") }),
            Candidate("c", "c", probe = { 600_000L }),
        )
        val s = joinedSession(resolver = { probes })
        s.socket.reply(newEpoch(rev = 2, epoch = 1, movie))
        advanceTimeBy(2_000)
        assertEquals("c", s.party.state.value.choice?.id)
        assertEquals(Match(MatchState.MATCHED, 0), s.party.state.value.match)
    }

    @Test
    fun aResolverThatThrowsACancellationOfItsOwnFailedAndOneTheSdkCancelledDidnt() = runTest {
        val gone = CompletableDeferred<List<Candidate<String>>>().apply { cancel() }
        val s = joinedSession(
            resolver = { media ->
                when (media.title) {
                    "Heat" -> gone.await()
                    "Ronin" -> withTimeout(1_000) { awaitCancellation() }
                    else -> awaitCancellation()
                }
            },
        )
        s.socket.reply(newEpoch(rev = 2, epoch = 1, movie))
        runCurrent()
        assertIs<CancellationException>(assertIs<PartyEvent.ResolveFailed>(s.events.single()).error)
        assertEquals(Match(MatchState.UNAVAILABLE), s.party.state.value.match)
        s.socket.reply(newEpoch(rev = 3, epoch = 2, movie.copy(title = "Ronin")))
        advanceTimeBy(1_001)
        assertIs<TimeoutCancellationException>(assertIs<PartyEvent.ResolveFailed>(s.events.last()).error)
        assertEquals(Match(MatchState.UNAVAILABLE), s.party.state.value.match)
        // A new epoch cancels the resolver call: no event.
        s.socket.reply(newEpoch(rev = 4, epoch = 3, movie.copy(title = "Collateral")))
        runCurrent()
        s.socket.reply(newEpoch(rev = 5, epoch = 4, movie.copy(title = "Ronin")))
        runCurrent()
        assertEquals(2, s.events.size)
    }

    @Test
    fun outOfRangeIntegersThrowBeforeAnyLocalEffect() = runTest {
        val a = TestPlayer()
        val s = boundSession(a)
        fun refused(block: () -> Unit) = assertEquals(PartyException.INVALID_MESSAGE, assertFailsWith<PartyException> { block() }.code)
        val max = (1L shl 53) - 1
        refused { s.party.setOffset(604_800_001) }
        refused { s.party.setOffset(-604_800_001) }
        assertEquals(0, s.party.state.value.offsetMs)
        s.party.setOffset(-604_800_000)
        assertEquals(-604_800_000, s.party.state.value.offsetMs)
        refused { s.party.seek(max + 1) }
        refused { s.party.seek(max) } // max − offset is beyond an int
        refused { s.party.setMedia(movie, "heat.mkv", positionMs = max + 1) }
        refused { s.party.setMedia(movie.copy(durationMs = 604_800_001), "heat.mkv") }
        runCurrent()
        assertEquals(PlayerState.READY, s.party.state.value.player, "still bound")
        a.positionMs = max // the adapter's position − offset is beyond an int
        refused { s.party.pause() }
        runCurrent()
        assertEquals(listOf("setRate 1.0"), a.log)
        val sent = s.socket.drain()
        assertTrue(sent.none { it == "seek" || it == "pause" || it == "setMedia" }, "$sent")
    }

    /** The `status` messages the client sent since the last read, skipping the others. */
    private fun FakeSocket.statuses(): List<JsonObject> =
        generateSequence { sent.tryReceive().getOrNull() }.filter { it["type"].asString() == "status" }.toList()

    /** An HTTP server that answers every GET with [answer], run by the test's scheduler (virtual time). */
    private fun TestScope.server(answer: suspend MockRequestHandleScope.(HttpRequestData) -> HttpResponseData): Pair<HttpClient, MockEngine> {
        val engine = MockEngine(
            MockEngineConfig().apply {
                dispatcher = StandardTestDispatcher(testScheduler)
                addHandler(answer)
            },
        )
        return HttpClient(engine) to engine
    }

    @Test
    fun aJoinedAnsweringJoinStartsTheBackgroundRefresh() = runTest {
        val (http, engine) = server { respondError(HttpStatusCode.NotFound) }
        joinedSession(relays = Relays.public(http))
        runCurrent()
        assertEquals(1, engine.requestHistory.size)
    }

    @Test
    fun afterCloseCreateAndJoinThrowIllegalStateException() = runTest {
        val f = fixture()
        f.party.close()
        assertFailsWith<IllegalStateException> { f.party.create() }
        assertFailsWith<IllegalStateException> { f.party.join("K7M-Q2X") }
        assertFailsWith<IllegalStateException> { f.party.join("K7M-Q2") }
        assertEquals(0, f.relay.opened)
    }

    @Test
    fun aRefusedSetMediaRebindsAndReportsAtOnce() = runTest {
        val a = TestPlayer()
        val s = boundSession(a)
        s.party.setMedia(movie.copy(title = "Ronin"), "ronin.mkv")
        s.socket.next("setMedia")
        advanceTimeBy(800)
        assertEquals(PlayerState.LOADING, s.party.state.value.player, "unbound while setMedia is pending")
        s.socket.statuses()
        a.held = CompletableDeferred() // the rebind's setRate doesn't finish
        s.socket.reply(error("rate_limited", "setMedia"))
        runCurrent()
        assertEquals(listOf("ready"), s.socket.statuses().map { it["player"].asString() })
        assertEquals(800, testScheduler.currentTime)
    }

    @Test
    fun playBlockedCarriesTheErrorThePlayerThrew() = runTest {
        val a = TestPlayer()
        val s = boundSession(a)
        val refusal = IllegalStateException("autoplay wants a user gesture")
        a.refusal = refusal
        s.socket.reply(snapshot(rev = 3, phase = "playing", atMs = relayNow()))
        runCurrent()
        assertSame(refusal, assertIs<PartyEvent.PlayBlocked>(s.events.single()).error)
    }

    @Test
    fun aPlayThatThrowsACancellationOfItsOwnRefusedToStart() = runTest {
        val a = TestPlayer()
        val s = boundSession(a)
        a.refusal = CancellationException("the player's own timeout")
        s.socket.reply(snapshot(rev = 3, phase = "playing", atMs = relayNow()))
        runCurrent()
        assertEquals(PlayerState.LOADING, s.party.state.value.player)
        assertIs<PartyEvent.PlayBlocked>(s.events.single())
        advanceTimeBy(600)
        assertEquals(2, a.log.count { it == "play" }, "the lane goes on: ${a.log}")
    }

    // -- resolveAgain (SDK.md §9.1) -----------------------------------------------------------------------------------

    @Test
    fun resolveAgainKeepsTheChoiceObjectWhenItsIdComesBackUnderAnotherRef() = runTest {
        var found = listOf(Candidate("a", "a.mkv", durationMs = 600_000))
        val s = joinedSession(resolver = { found })
        s.socket.reply(newEpoch(rev = 2, epoch = 1, movie))
        runCurrent()
        val chosen = assertNotNull(s.party.state.value.choice)
        found = listOf(Candidate("a", "a2.mkv", durationMs = 600_000))
        s.party.resolveAgain()
        runCurrent()
        assertSame(chosen, s.party.state.value.choice)
    }

    // -- App input is copied (SDK.md §16.6) ---------------------------------------------------------------------------

    @Test
    fun changingTheAppsMapsAfterSetMediaChangesNothing() = runTest {
        val resolved = mutableListOf<String>()
        val s = joinedSession(host = true, resolver = { media -> resolved += media.title; emptyList() })
        val ids = mutableMapOf("imdb" to "tt0113277")
        val hints = mutableMapOf<String, JsonElement>("com.example" to JsonPrimitive(1))
        s.party.setMedia(movie.copy(ids = ids, ext = JsonObject(hints)), "heat.mkv")
        ids["imdb"] = "tt9999999"
        hints["com.example"] = JsonPrimitive(2)
        val sent = assertNotNull(s.socket.next("setMedia")["media"])
        val shared = movie.copy(ids = mapOf("imdb" to "tt0113277"), ext = buildJsonObject { put("com.example", 1) })
        assertEquals(shared.toJson(), sent)
        // The relay shares what was sent: the party's own media.
        s.socket.reply(snapshot(rev = 2, phase = "paused", media = sent))
        runCurrent()
        assertOwnMedia(s.party.state.value, shared)
        assertEquals(emptyList(), resolved)
    }

    private companion object {
        /** A token as the relay makes one: 43 base64url characters. */
        const val TOKEN = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
    }
}
