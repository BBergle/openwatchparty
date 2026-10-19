package party.openwatch

import io.ktor.client.HttpClient
import io.ktor.client.request.prepareGet
import io.ktor.client.statement.bodyAsChannel
import io.ktor.http.HttpStatusCode
import io.ktor.utils.io.ByteReadChannel
import io.ktor.utils.io.readAvailable
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement

/** One relay of a list (SPEC.md §5.2): [prefixes] are the first symbols of the codes it owns. */
public data class Relay(val id: String, val name: String, val url: String, val prefixes: String)

/**
 * The relays a party connects to (SDK.md §3): the public relays, whose list is refreshed only from
 * `https://openwatch.party/relays.json`, or one self-hosted relay. An app keeps one and passes it to every party; the
 * list and its refresh schedule belong to it. The parties open their WebSockets with [http]; the public list is
 * fetched through a copy of it that follows no redirects.
 */
public class Relays private constructor(
    /** The app's Ktor client, with WebSockets installed. */
    internal val http: HttpClient,
    /** The self-hosted relay; null for the public relays. */
    private val custom: Relay?,
    /** Measures the age of the last successful refresh. */
    private val clock: MonotonicClock,
) {
    private val getter: HttpClient by lazy { http.config { followRedirects = false } }

    private var current: List<Relay> =
        if (custom != null) listOf(custom) else parseRelayList(Json.parseToJsonElement(PUBLIC_RELAYS_JSON))!!

    /** [clock] time of the last successful refresh of any kind; null before the first. */
    private var lastRefreshMs: Long? = null
    private var backgroundRunning = false

    /** The list in use: the public list, or the self-hosted relay alone. */
    public val list: List<Relay> get() = current

    /** The relay that owns [code], normalized first; null for an invalid code or a symbol nobody owns. */
    public fun relayFor(code: String): Relay? = normalizeCode(code)?.let { route(it, current) }

    /**
     * A forced refresh of the public list (SDK.md §3.3); false when the source gave no valid list. A self-hosted relay
     * has no list to refresh: true at once.
     */
    public suspend fun refresh(): Boolean = custom != null || fetch()

    /**
     * The background refresh of the public list when a party gets `joined` (SDK.md §3.3): due when no refresh of any
     * kind has succeeded yet, or the last success is 24 h old. Never throws, and never delays [refresh].
     */
    internal suspend fun refreshInBackground() {
        val last = lastRefreshMs
        if (custom != null || backgroundRunning || (last != null && clock.nowMs() - last < Owp.LIST_REFRESH_MS)) return
        backgroundRunning = true
        try {
            fetch()
        } finally {
            backgroundRunning = false
        }
    }

    /** One GET of the public list; a valid list replaces the one in use. */
    private suspend fun fetch(): Boolean {
        val relays = parseRelayList(httpGetJson(Owp.PUBLIC_LIST_URL)) ?: return false
        current = relays
        lastRefreshMs = clock.nowMs()
        return true
    }

    /**
     * A GET as SDK.md §3.4 allows it: aborted after 5 s, no redirects, status 200 only, at most 64 KiB of UTF-8 JSON
     * body. Null when any of that fails.
     */
    private suspend fun httpGetJson(url: String): JsonElement? {
        val body = withTimeoutOrNull(Owp.HTTP_TIMEOUT_MS) {
            try {
                getter.prepareGet(url).execute { response ->
                    if (response.status == HttpStatusCode.OK) response.bodyAsChannel().readUtf8(Owp.HTTP_MAX_BODY_BYTES) else null
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                null
            }
        } ?: return null
        return try {
            Json.parseToJsonElement(body)
        } catch (e: Exception) {
            null
        }
    }

    public companion object {
        /** `https://` or `http://`, lower case, and a host: something before the first `/`, `?` or `#`. */
        private val BASE_URL = Regex("^https?://[^/?#]+")

        /** The public relays, starting from the list compiled into the SDK. */
        public fun public(http: HttpClient): Relays = public(http, MonotonicClock.System)

        /**
         * The self-hosted relay at [baseUrl] (`https://relay.example.com`, `http://192.168.1.5:8080/owp`), for every room:
         * its WebSocket URL is the base with `wss://` for `https://` or `ws://` for `http://`, plus `/v1/ws`. Its [list]
         * is that relay alone, as `custom`, owning every code.
         *
         * @throws PartyException `invalid_message` when [baseUrl], without trailing `/`s, isn't `https://` or `http://`
         *   followed by a host, or gives a WebSocket URL that SDK.md §3.2 refuses.
         */
        public fun at(http: HttpClient, baseUrl: String): Relays {
            val base = baseUrl.trimEnd('/')
            if (!BASE_URL.containsMatchIn(base)) throw PartyException(PartyException.INVALID_MESSAGE, "not an http(s) base URL: $baseUrl")
            val url = (if (base.startsWith("https://")) "wss://" + base.removePrefix("https://") else "ws://" + base.removePrefix("http://")) + "/v1/ws"
            if (!validRelayUrl(url)) throw PartyException(PartyException.INVALID_MESSAGE, "not a relay base URL: $baseUrl")
            return Relays(http, Relay("custom", base, url, Owp.ALPHABET), MonotonicClock.System)
        }

        internal fun public(http: HttpClient, clock: MonotonicClock): Relays = Relays(http, null, clock)
    }
}

/**
 * The whole body as text; null when it is longer than [limit] bytes, where reading stops. Throws when it isn't UTF-8.
 */
private suspend fun ByteReadChannel.readUtf8(limit: Int): String? {
    val buffer = ByteArray(limit + 1)
    var size = 0
    while (size < buffer.size) {
        val n = readAvailable(buffer, size, buffer.size - size)
        if (n < 0) break
        size += n
    }
    return if (size > limit) null else buffer.decodeToString(0, size, throwOnInvalidSequence = true)
}

/** The `relays` of a public list document (fetched or embedded), validated as SDK.md §3.2 says; null when invalid. */
internal fun parseRelayList(doc: JsonElement?): List<Relay>? {
    val items = doc.asObject()?.get("relays").asArray() ?: return null
    if (items.isEmpty()) return null
    val ids = mutableSetOf<String>()
    val owned = mutableSetOf<Char>()
    return items.map { item ->
        val r = item.asObject() ?: return null
        val id = r["id"].asString()?.takeIf { validRelayId(it) && ids.add(it) } ?: return null
        val name = r["name"].asString()?.takeIf(::validRelayName) ?: return null
        val url = r["url"].asString()?.takeIf { it.startsWith("wss://") && validRelayUrl(it) } ?: return null
        val prefixes = r["prefixes"].asString()?.takeIf { it.isNotEmpty() } ?: return null
        for (c in prefixes) if (Owp.ALPHABET.indexOf(c) < 0 || !owned.add(c)) return null
        Relay(id, name, url, prefixes)
    }
}

private fun validRelayId(id: String): Boolean =
    id.length in 1..32 && id[0] != '-' && id.all { it in 'a'..'z' || it in '0'..'9' || it == '-' }

/** 1–64 code points without forbidden characters or unpaired surrogates (SPEC.md §5.2). */
private fun validRelayName(name: String): Boolean = textProblem(name, 1, 64) == null

/**
 * A relay URL as SDK.md §3.2 shapes it: `wss://` or `ws://`, then printable ASCII other than `?` and `#`, with a host,
 * ending in `/v1/ws`.
 */
internal fun validRelayUrl(url: String): Boolean {
    val rest = when {
        url.startsWith("wss://") -> url.removePrefix("wss://")
        url.startsWith("ws://") -> url.removePrefix("ws://")
        else -> return false
    }
    return rest.none { it.code !in 0x21..0x7e || it == '?' || it == '#' } && rest.endsWith("/v1/ws") && rest.substringBefore('/').isNotEmpty()
}
