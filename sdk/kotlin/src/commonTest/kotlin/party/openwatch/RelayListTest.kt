package party.openwatch

import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockEngineConfig
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.respondError
import io.ktor.client.request.HttpRequestData
import io.ktor.client.request.HttpResponseData
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/** The relay list's sources, refresh and HTTP rules (SDK.md §3), against a mock HTTP server in virtual time. */
@OptIn(ExperimentalCoroutinesApi::class)
class RelayListTest {
    /** SDK.md §3.1: the compiled-in list is a copy of relays.json at the repository root. */
    @Test
    fun embeddedListEqualsTheCanonicalFile() {
        assertEquals(TestData.publicRelays, PUBLIC_RELAYS_JSON, "PublicRelays.kt differs from relays.json")
    }

    @Test
    fun embeddedListIsValid() {
        val relays = assertNotNull(parseRelayList(Json.parseToJsonElement(PUBLIC_RELAYS_JSON)))
        assertEquals(listOf("na", "eu", "asia"), relays.map { it.id })
    }

    /** An HTTP server that answers every GET with [answer], run by the test's scheduler. */
    private fun TestScope.server(answer: suspend MockRequestHandleScope.(HttpRequestData) -> HttpResponseData): Pair<HttpClient, MockEngine> {
        val engine = MockEngine(
            MockEngineConfig().apply {
                dispatcher = StandardTestDispatcher(testScheduler)
                addHandler(answer)
            },
        )
        return HttpClient(engine) to engine
    }

    private val MockEngine.asked: List<String> get() = requestHistory.map { it.url.toString() }

    @Test
    fun thePublicListComesOnlyFromOpenwatchParty() = runTest {
        val (http, engine) = server { respond(list(relay("x", "wss://x.example/v1/ws", Owp.ALPHABET))) }
        val relays = Relays.public(http)
        assertEquals(listOf("na", "eu", "asia"), relays.list.map { it.id }, "the embedded list until a refresh")
        assertTrue(relays.refresh())
        assertEquals(listOf("https://openwatch.party/relays.json"), engine.asked)
        assertEquals(listOf("x"), relays.list.map { it.id })
    }

    @Test
    fun aPublicListWithAWsUrlIsRefused() = runTest {
        val (http, _) = server { respond(list(relay("x", "ws://x.example/v1/ws", Owp.ALPHABET))) }
        val relays = Relays.public(http)
        assertFalse(relays.refresh())
        assertEquals(listOf("na", "eu", "asia"), relays.list.map { it.id }, "nothing changed")
    }

    @Test
    fun theBackgroundRefreshIsDueUntilARefreshSucceedsAndThenEvery24Hours() = runTest {
        var answers = false
        val (http, engine) = server {
            if (answers) respond(list(relay("x", "wss://x.example/v1/ws", Owp.ALPHABET))) else respondError(HttpStatusCode.NotFound)
        }
        var now = 0L
        val relays = Relays.public(http) { now }
        relays.refreshInBackground()
        assertEquals(1, engine.asked.size, "the first party connecting refreshes")
        now = 1
        relays.refreshInBackground()
        assertEquals(2, engine.asked.size, "a failed refresh doesn't count")
        answers = true
        now = 2
        assertTrue(relays.refresh())
        relays.refreshInBackground()
        assertEquals(3, engine.asked.size, "a forced refresh counts")
        now = 2 + Owp.LIST_REFRESH_MS - 1
        relays.refreshInBackground()
        assertEquals(3, engine.asked.size)
        now = 2 + Owp.LIST_REFRESH_MS
        relays.refreshInBackground()
        assertEquals(4, engine.asked.size)
        now = 2 + Owp.LIST_REFRESH_MS + 1
        relays.refreshInBackground()
        assertEquals(4, engine.asked.size, "a background refresh counts")
        assertEquals(setOf(Owp.PUBLIC_LIST_URL), engine.asked.toSet())
    }

    @Test
    fun aBaseUrlIsOneRelayForEveryCodeAndNeverFetches() = runTest {
        val (http, engine) = server { respond(list(relay("x", "wss://x.example/v1/ws", Owp.ALPHABET))) }
        for ((base, url) in listOf(
            "https://relay.example.com" to "wss://relay.example.com/v1/ws",
            "http://192.168.1.5:8080/owp//" to "ws://192.168.1.5:8080/owp/v1/ws",
        )) {
            val relays = Relays.at(http, base)
            val one = Relay("custom", base.trimEnd('/'), url, Owp.ALPHABET)
            assertEquals(listOf(one), relays.list)
            assertEquals(one, relays.relayFor("2a2-222"))
            assertEquals(one, relays.relayFor("zzzzzz"))
            assertEquals(null, relays.relayFor("2a2-22"))
            assertTrue(relays.refresh())
            relays.refreshInBackground()
            assertEquals(listOf(one), relays.list)
        }
        assertEquals(emptyList<String>(), engine.asked, "no GET")
    }

    @Test
    fun aBaseThatIsNotAnHttpUrlWithAHostThrowsInvalidMessage() {
        val http = HttpClient(MockEngine { respondError(HttpStatusCode.NotFound) })
        val refused = listOf(
            "", "relay.example.com", "ftp://relay.example.com", "HTTPS://relay.example.com", "https://", "https:///owp", "http://?x",
            "https://#x", "https://relay example.com", "https://relay.example.com/owp?x", "https://relay.example.com/owp#x",
        )
        for (base in refused) {
            assertEquals(PartyException.INVALID_MESSAGE, assertFailsWith<PartyException>(base) { Relays.at(http, base) }.code, base)
        }
        for (base in listOf("https://relay.example.com", "http://192.168.1.5:8080/owp/", "https://relay.example.com//")) Relays.at(http, base)
    }

    @Test
    fun aBodyOfAtMost65536BytesIsRead() = runTest {
        val x = relay("x", "wss://x.example/v1/ws", Owp.ALPHABET)
        fun padded(size: Int): String {
            val empty = """{"relays":[$x],"pad":""}"""
            return """{"relays":[$x],"pad":"${"p".repeat(size - empty.length)}"}"""
        }
        val (http, _) = server { respond(padded(Owp.HTTP_MAX_BODY_BYTES)) }
        assertTrue(Relays.public(http).refresh())
        val (tooLong, _) = server { respond(padded(Owp.HTTP_MAX_BODY_BYTES + 1)) }
        assertFalse(Relays.public(tooLong).refresh())
    }

    @Test
    fun aRedirectIsNotFollowed() = runTest {
        val (http, engine) = server { request ->
            if (request.url.host == "openwatch.party") {
                respond("", HttpStatusCode.Found, headersOf(HttpHeaders.Location, "https://evil.example/relays.json"))
            } else {
                respond(list(relay("x", "wss://x.example/v1/ws", Owp.ALPHABET)))
            }
        }
        val relays = Relays.public(http)
        assertFalse(relays.refresh())
        assertEquals(listOf(Owp.PUBLIC_LIST_URL), engine.asked)
        assertEquals(listOf("na", "eu", "asia"), relays.list.map { it.id })
    }

    @Test
    fun anErrorStatusOrABodyThatIsNotUtf8JsonFails() = runTest {
        val good = list(relay("x", "wss://x.example/v1/ws", Owp.ALPHABET))
        val (error, _) = server { respond(good, HttpStatusCode.InternalServerError) }
        assertFalse(Relays.public(error).refresh())
        val x = relay("x", "wss://x.example/v1/ws", Owp.ALPHABET)
        val invalid = """{"relays":[$x],"pad":"""".encodeToByteArray() + byteArrayOf(0xff.toByte()) + "\"}".encodeToByteArray()
        val (notUtf8, _) = server { respond(invalid) }
        assertFalse(Relays.public(notUtf8).refresh())
        val (notJson, _) = server { respond("relays: x") }
        assertFalse(Relays.public(notJson).refresh())
    }

    @Test
    fun aGetIsAbortedAfter5Seconds() = runTest {
        val started = CompletableDeferred<Unit>()
        val cancelled = CompletableDeferred<Unit>()
        val (http, _) = server {
            started.complete(Unit)
            try {
                awaitCancellation()
            } finally {
                cancelled.complete(Unit)
            }
        }
        assertFalse(Relays.public(http).refresh())
        assertEquals(Owp.HTTP_TIMEOUT_MS, testScheduler.currentTime)
        assertTrue(started.isCompleted)
        cancelled.await()
    }

    private fun list(vararg relays: String) = """{"relays":[${relays.joinToString(",")}]}"""

    private fun relay(id: String, url: String, prefixes: String) =
        """{"id":"$id","name":"${id.uppercase()}","url":"$url","prefixes":"$prefixes"}"""
}
