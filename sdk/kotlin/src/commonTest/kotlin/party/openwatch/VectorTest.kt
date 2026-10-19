package party.openwatch

import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respondError
import io.ktor.http.HttpStatusCode
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlin.math.roundToLong
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlin.test.fail

/** Every table of conformance/vectors (SDK.md §17), run against the SDK's own functions. */
class VectorTest {
    private class Case(val name: String, val input: JsonObject, val expect: JsonObject)

    private fun cases(table: String): List<Case> {
        val text = TestData.vectors["$table.json"] ?: fail("conformance/vectors/$table.json is missing")
        val doc = Json.parseToJsonElement(text).asObject()!!
        assertEquals(table, doc["table"].asString())
        val cases = doc["cases"].asArray()!!.map { c ->
            val o = c.asObject()!!
            Case(o["name"].asString()!!, o["input"].asObject()!!, o["expect"].asObject()!!)
        }
        assertTrue(cases.isNotEmpty(), "$table has no cases")
        return cases
    }

    /** Runs [check] on every case and fails once with every failing case's name and message. */
    private inline fun forEach(table: String, check: (Case) -> Unit) {
        val failures = mutableListOf<String>()
        for (case in cases(table)) {
            try {
                check(case)
            } catch (e: AssertionError) {
                failures += "${case.name}: ${e.message}"
            }
        }
        if (failures.isNotEmpty()) fail("$table: ${failures.size} failing case(s)\n" + failures.joinToString("\n"))
    }

    private fun JsonElement?.long(): Long = asInt() ?: fail("not an int: $this")

    private fun JsonElement?.longOrNull(): Long? = if (this == null || this is kotlinx.serialization.json.JsonNull) null else long()

    private fun JsonElement?.obj(): JsonObject = asObject() ?: fail("not an object: $this")

    private fun JsonElement?.str(): String = asString() ?: fail("not a string: $this")

    private fun JsonElement?.strOrNull(): String? = asString()

    private fun JsonElement?.bool(): Boolean = asBoolean() ?: fail("not a boolean: $this")

    private fun JsonElement?.num(): Double = asNumber() ?: fail("not a number: $this")

    @Test
    fun timeline() = forEach("timeline") { case ->
        val room = case.input["room"].obj()
        val phase = Phase.of(room["phase"].str())!!
        val atMs = room["atMs"].long()
        val now = case.input["serverNowMs"].long()
        val position = roomPosition(phase, room["positionMs"].long(), atMs, room["rate"].num(), case.input["durationMs"].long(), now)
        assertEquals(case.expect["positionMs"].long(), position, "positionMs")
        assertEquals(case.expect["running"].bool(), roomRunning(phase, atMs, now), "running")
    }

    @Test
    fun clockSync() = forEach("clock-sync") { case ->
        val sync = ClockSync()
        val results = mutableListOf<String>()
        for (event in case.input["events"].asArray()!!) {
            val e = event.obj()
            if ("ping" in e) {
                sync.ping(e["ping"].long(), e["atMs"].long())
            } else {
                val result = sync.pong(e["pong"].long(), e["serverTime"].long(), e["atMs"].long())
                results += "${result.name.lowercase()} ${sync.best?.offsetMs} ${sync.best?.rttMs}"
            }
        }
        val expected = case.expect["pongs"].asArray()!!.map {
            val p = it.obj()
            "${p["result"].str()} ${p["offsetMs"].longOrNull()} ${p["rttMs"].longOrNull()}"
        }
        assertEquals(expected, results)
    }

    @Test
    fun reconcile() = forEach("reconcile") { case ->
        val room = case.input["room"].obj()
        val player = case.input["player"].obj()
        val out = reconcile(
            StepInput(
                phase = Phase.of(room["phase"].str())!!,
                positionMs = room["positionMs"].long(),
                atMs = room["atMs"].long(),
                rate = room["rate"].num(),
                durationMs = case.input["durationMs"].long(),
                nowMs = case.input["serverNowMs"].long(),
                offsetMs = case.input["offsetMs"].long(),
                playerPositionMs = player["positionMs"].long(),
                paused = player["paused"].bool(),
                buffering = player["buffering"].bool(),
                playerDurationMs = player["durationMs"].longOrNull(),
                canSetRate = player["canSetRate"].bool(),
                seekAccuracyMs = player["seekAccuracyMs"]?.long() ?: 0,
                appliedRate = player["rate"].num(),
                nudging = case.input["nudging"].bool(),
                sinceSeekMs = case.input["sinceSeekMs"].longOrNull(),
            ),
        )
        val actions = out.actions.map {
            when (it) {
                StepAction.Pause -> "pause"
                is StepAction.SetRate -> "setRate ${micro(it.rate)}"
                is StepAction.Seek -> "seek ${it.positionMs}"
                StepAction.Play -> "play"
            }
        }
        val expected = case.expect["actions"].asArray()!!.map {
            val a = it.obj()
            when (val kind = a["do"].str()) {
                "setRate" -> "setRate ${micro(a["rate"].num())}"
                "seek" -> "seek ${a["positionMs"].long()}"
                else -> kind
            }
        }
        assertEquals(expected, actions, "actions")
        assertEquals(case.expect["nudging"].bool(), out.nudging, "nudging")
        assertEquals(case.expect["playInMs"].longOrNull(), out.playInMs, "playInMs")
    }

    private fun micro(rate: Double): Long = (rate * 1e6).roundToLong()

    @Test
    @OptIn(ExperimentalCoroutinesApi::class)
    fun matching() = runTest {
        val clock = MonotonicClock { testScheduler.currentTime }
        forEach("matching") { case ->
            val media = parseMedia(case.input["media"].obj())
            val candidates = case.input["candidates"].asArray()!!.map {
                val c = it.obj()
                val probe = c["probe"].asObject()?.let { p ->
                    val duration = p["durationMs"].longOrNull()
                    val takes = p["takesMs"].long()
                    suspend {
                        delay(takes)
                        duration
                    }
                }
                Candidate(
                    id = c["id"].str(),
                    ref = c["id"].str(),
                    durationMs = c["durationMs"].longOrNull(),
                    sizeBytes = c["sizeBytes"].longOrNull(),
                    oshash = c["oshash"].strOrNull(),
                    name = c["name"].strOrNull(),
                    probe = probe,
                )
            }
            var published: List<Ranked<String>> = emptyList()
            val result = walk(media, candidates, clock) { published = it }
            assertEquals(case.expect["order"].asArray()!!.map { it.str() }, result.ranked.map { it.id }, "order")
            val expectedCandidates = case.expect["candidates"].asArray()!!.map { it.obj()["id"].str() to it.obj()["deltaMs"].longOrNull() }
            assertEquals(expectedCandidates, published.map { it.candidate.id to it.deltaMs }, "candidates")
            assertEquals(case.expect["probed"].asArray()!!.map { it.str() }, result.probed, "probed")
            val (outcome, choice, delta) = when (val o = result.outcome) {
                is WalkOutcome.Matched -> Triple("matched", o.candidate.id, o.deltaMs)
                is WalkOutcome.Mismatch -> Triple("mismatch", o.candidate.id, o.deltaMs)
                WalkOutcome.Unavailable -> Triple("unavailable", null, null)
            }
            assertEquals(case.expect["result"].str(), outcome, "result")
            assertEquals(case.expect["choice"].strOrNull(), choice, "choice")
            assertEquals(case.expect["deltaMs"].longOrNull(), delta, "deltaMs")
        }
    }

    @Test
    fun sameTitle() = forEach("same-title") { case ->
        val a = parseMedia(case.input["a"].obj())
        val b = parseMedia(case.input["b"].obj())
        val same = case.expect["same"].bool()
        assertEquals(same, sameTitle(a, b), "sameTitle(a, b)")
        assertEquals(same, sameTitle(b, a), "sameTitle(b, a)")
    }

    @Test
    fun codeRouting() = forEach("code-routing") { case ->
        val relays = case.input["relays"].asArray()!!.map {
            val r = it.obj()
            Relay(r["id"].str(), r["id"].str(), "wss://${r["id"].str()}.example/v1/ws", r["prefixes"].str())
        }
        val code = normalizeCode(case.input["code"].str())
        val error = case.expect["error"].strOrNull()
        if (error != null) {
            assertEquals(PartyException.INVALID_CODE, error)
            assertEquals(null, code, "code")
            val thrown = runCatching { formatCode(case.input["code"].str()) }.exceptionOrNull()
            assertEquals(PartyException.INVALID_CODE, (thrown as? PartyException)?.code, "formatCode")
        } else {
            val expected = case.expect["code"].str()
            assertEquals(expected, code, "code")
            assertEquals(case.expect["relay"].strOrNull(), route(expected, relays)?.id, "relay")
            assertEquals(expected.substring(0, 3) + "-" + expected.substring(3), formatCode(case.input["code"].str()))
        }
    }

    @Test
    fun relayList() = forEach("relay-list") { case ->
        val relays = parseRelayList(case.input["list"])
        val error = case.expect["error"].strOrNull()
        if (error != null) {
            assertEquals("invalid_list", error)
            assertEquals(null, relays, "relays")
        } else {
            val expected = case.expect["relays"].asArray()!!.map {
                val r = it.obj()
                listOf(r["id"].str(), r["name"].str(), r["url"].str(), r["prefixes"].str())
            }
            assertEquals(expected, relays?.map { listOf(it.id, it.name, it.url, it.prefixes) }, "relays")
        }
    }

    /** The constructor refuses what names.json marks invalid with `invalid_message` (SDK.md §7.1). */
    @Test
    fun names() {
        val http = HttpClient(MockEngine { respondError(HttpStatusCode.NotFound) })
        val relays = Relays.public(http)
        val scope = CoroutineScope(Job())
        try {
            forEach("names") { case ->
                val value = case.input["value"].str()
                val (name, client) = when (val field = case.input["field"].str()) {
                    "name" -> value to "Test/1.0"
                    "client" -> "Ben" to value
                    else -> fail("unknown field $field")
                }
                val thrown = runCatching { WatchParty(relays, client, name, { emptyList<Candidate<String>>() }, scope) }.exceptionOrNull()
                if (case.expect["valid"].bool()) {
                    assertEquals(null, thrown, "the constructor threw")
                } else {
                    assertEquals(PartyException.INVALID_MESSAGE, (thrown as? PartyException)?.code, "the constructor threw $thrown")
                }
            }
        } finally {
            scope.cancel()
            http.close()
        }
    }

    /**
     * The `room` check of SDK.md §8: the snapshot and the `action`, when present. A `room` that fails it is dropped.
     * The `media` member is not part of it: the party checks that only on a new epoch.
     */
    @Test
    fun roomCheck() = forEach("room-check") { case ->
        assertEquals(case.expect["valid"].bool(), case.input["room"].asObject()?.let { In.room(it) } != null, "valid")
    }

    /**
     * The descriptor checks of SDK.md §9.7: on receive [parseMedia], which throws `invalid_message`; on send
     * [checkMedia] on the [Media] the app built, sizes included. A send case with a number beyond an int has no [Media]
     * to build here; it must be invalid, and WireTest covers [checkMedia]'s range rule on such a value.
     */
    @Test
    fun mediaCheck() = forEach("media-check") { case ->
        val json = case.input["media"] ?: fail("no media")
        val valid = when (val on = case.input["on"].str()) {
            "receive" -> {
                val refused = runCatching { parseMedia(json) }.exceptionOrNull()
                if (refused != null) assertEquals(PartyException.INVALID_MESSAGE, (refused as? PartyException)?.code, "parseMedia threw $refused")
                refused == null
            }
            "send" -> readMedia(json.obj())?.let { checkMedia(it) == null } ?: false
            else -> fail("unknown direction $on")
        }
        assertEquals(case.expect["valid"].bool(), valid, "valid")
    }

    /** The party traces, each run by the driver in PartyTraces.kt against a real WatchParty in virtual time. */
    @Test
    fun party() = forEach("party") { case -> runPartyTrace(case.input, case.expect) }

    /** A new table in conformance/vectors fails here until this runner reads it. */
    @Test
    fun everyTableRuns() {
        val run = setOf(
            "timeline", "clock-sync", "reconcile", "matching", "same-title", "code-routing", "relay-list", "names",
            "room-check", "media-check", "party",
        )
        assertEquals(run, TestData.vectors.keys.map { it.removeSuffix(".json") }.toSet())
    }
}
