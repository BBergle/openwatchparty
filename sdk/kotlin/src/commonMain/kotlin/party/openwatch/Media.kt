package party.openwatch

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/** The media descriptor's `kind` (SPEC.md §10). */
public enum class MediaKind(public val wire: String) {
    MOVIE("movie"),
    EPISODE("episode"),

    /** A file with no catalogue identity, such as a home video, a recording or a clip. */
    VIDEO("video"),
    ;

    internal companion object {
        fun of(wire: String?): MediaKind? = entries.firstOrNull { it.wire == wire }
    }
}

/** An episode's series. */
public data class Series(
    val title: String,
    val year: Long? = null,
    /**
     * The series' IDs, keyed by namespace.
     *
     * The v1 namespaces are `imdb`, `tmdb`, `tvdb`, `plex`, `anilist`, `anidb`, `kitsu` and `mal`.
     */
    val ids: Map<String, String> = emptyMap(),
)

/** File hints that pick the same release among candidates. */
public data class MediaFile(
    val sizeBytes: Long? = null,
    /** The OpenSubtitles hash: 16 lower-case hex digits. */
    val oshash: String? = null,
    /**
     * The file name: a hint, never a path.
     *
     * It holds no directories, so no `/` or `\`, and no forbidden characters. It is never `.` or `..`.
     */
    val name: String? = null,
)

/**
 * The media descriptor: the title the room plays (SPEC.md §10).
 *
 * Positions are in the timeline of the reference file, whose duration is [durationMs].
 */
public data class Media(
    val kind: MediaKind,
    /** The movie's or episode's title, or the video's. */
    val title: String,
    /** The reference file's duration, from 1 to 604800000 (7 days). */
    val durationMs: Long,
    val year: Long? = null,
    /**
     * The IDs, keyed by namespace.
     *
     * For an episode, these are the episode's own IDs.
     */
    val ids: Map<String, String> = emptyMap(),
    /** The episode's series, which an episode requires. */
    val series: Series? = null,
    /** The season number as the setter's source numbers it, with 0 for specials. */
    val season: Long? = null,
    val episode: Long? = null,
    val file: MediaFile? = null,
    /**
     * App-private hints, keyed by a reverse-DNS app name such as `com.example` or `org.example`.
     *
     * The hints take at most 1024 bytes.
     */
    val ext: JsonObject? = null,
) {
    /** Encodes the descriptor as `setMedia` sends it. */
    public fun toJson(): JsonObject = buildJsonObject {
        put("kind", kind.wire)
        put("title", title)
        year?.let { put("year", it) }
        if (ids.isNotEmpty()) put("ids", stringMap(ids))
        series?.let { s ->
            put(
                "series",
                buildJsonObject {
                    put("title", s.title)
                    s.year?.let { put("year", it) }
                    if (s.ids.isNotEmpty()) put("ids", stringMap(s.ids))
                },
            )
        }
        season?.let { put("season", it) }
        episode?.let { put("episode", it) }
        put("durationMs", durationMs)
        file?.let { f ->
            put(
                "file",
                buildJsonObject {
                    f.sizeBytes?.let { put("sizeBytes", it) }
                    f.oshash?.let { put("oshash", it) }
                    f.name?.let { put("name", it) }
                },
            )
        }
        ext?.let { put("ext", it) }
    }
}

/**
 * Reads a descriptor with the receive checks of SDK.md §9.7, the way the SDK reads `media` and `joined`.
 *
 * - Unknown members are dropped.
 * - Unknown `ids` namespaces and all of `ext` are kept.
 * - The sizes aren't checked.
 *
 * Descriptors come from other participants, so treat every string as untrusted text.
 *
 * @throws PartyException `invalid_message` when [json] isn't an object, a member has the wrong type, a required one is
 *   missing, or a value breaks a rule of [checkMedia] other than the sizes.
 */
public fun parseMedia(json: JsonElement): Media {
    val media = (json as? JsonObject)?.let(::readMedia)
        ?: throw PartyException(PartyException.INVALID_MESSAGE, "the descriptor misses a member or has one of the wrong type")
    mediaProblem(media, sizes = false)?.let { throw PartyException(PartyException.INVALID_MESSAGE, it) }
    return media
}

/** [parseMedia] for the SDK's own reading: null instead of a throw. */
internal fun receivedMedia(json: JsonObject): Media? = readMedia(json)?.takeIf { mediaProblem(it, sizes = false) == null }

/** [o] as a [Media] when every member has its type and the required ones are there; values unchecked. */
internal fun readMedia(o: JsonObject): Media? {
    val kind = MediaKind.of(o["kind"].asString()) ?: return null
    val title = o["title"].asString() ?: return null
    val durationMs = o["durationMs"].asInt() ?: return null
    val typed = o.typed("year") { it.asInt() != null } &&
        o.typed("ids") { isStringMap(it) } &&
        o.typed("series") { s ->
            s is JsonObject && s["title"].asString() != null && s.typed("year") { it.asInt() != null } && s.typed("ids") { isStringMap(it) }
        } &&
        o.typed("season") { it.asInt() != null } &&
        o.typed("episode") { it.asInt() != null } &&
        o.typed("file") { f ->
            f is JsonObject && f.typed("sizeBytes") { it.asInt() != null } &&
                f.typed("oshash") { it.asString() != null } && f.typed("name") { it.asString() != null }
        } &&
        o.typed("ext") { it is JsonObject }
    if (!typed) return null
    return Media(
        kind = kind,
        title = title,
        durationMs = durationMs,
        year = o["year"].asInt(),
        ids = o["ids"].asStringMap().orEmpty(),
        series = o["series"].asObject()?.let { s ->
            Series(s["title"].asString()!!, s["year"].asInt(), s["ids"].asStringMap().orEmpty())
        },
        season = o["season"].asInt(),
        episode = o["episode"].asInt(),
        file = o["file"].asObject()?.let { f ->
            MediaFile(f["sizeBytes"].asInt(), f["oshash"].asString(), f["name"].asString())
        },
        ext = o["ext"].asObject(),
    )
}

/**
 * The SDK's own deep copy of [media] (SDK.md §16.6): maps and JSON the app may still change behind it are copied, so
 * the party never sees a later change.
 */
internal fun ownCopy(media: Media): Media = media.copy(
    ids = media.ids.toMap(),
    series = media.series?.let { it.copy(ids = it.ids.toMap()) },
    ext = media.ext?.let { copyJson(it) as JsonObject },
)

/** A deep copy of [e] whose maps and lists nobody else holds. */
private fun copyJson(e: JsonElement): JsonElement = when (e) {
    is JsonObject -> JsonObject(e.mapValues { (_, v) -> copyJson(v) })
    is JsonArray -> JsonArray(e.map { copyJson(it) })
    is JsonPrimitive -> e
}

/** True when [key] is absent or passes [check]. */
private inline fun JsonObject.typed(key: String, check: (JsonElement) -> Boolean): Boolean = get(key)?.let(check) ?: true

private fun isStringMap(json: JsonElement): Boolean = json is JsonObject && json.values.all { it.asString() != null }

private fun stringMap(map: Map<String, String>): JsonObject = JsonObject(map.mapValues { JsonPrimitive(it.value) })

private val OSHASH = Regex("^[0-9a-f]{16}$")
private val EXT_NAME = Regex("^[a-z0-9-]+(\\.[a-z0-9-]+)+$")

/**
 * Checks [media] against the send checks of SDK.md §9.7, and returns null when it passes.
 *
 * Otherwise it returns the first rule the descriptor breaks. The rules are checked in this order:
 *
 * 1. The `title`.
 * 2. The `durationMs`, which is 1 to 604800000 (7 days).
 * 3. The `series`, which an episode needs.
 * 4. The ranges of the numbers.
 * 5. The `oshash`.
 * 6. The `file.name`, which has no `/`, `\` or forbidden character, and is not `.` or `..`.
 * 7. The IDs.
 * 8. Unpaired UTF-16 surrogates, in any string or member name.
 * 9. The names in `ext`.
 * 10. The sizes: at most 1024 bytes of `ext` and 4096 bytes in all, measured on the SDK's compact JSON.
 *
 * To fix a descriptor, an app trims `file.name` or `ext` and checks again.
 */
public fun checkMedia(media: Media): String? = mediaProblem(media, sizes = true)

/** The rules of SDK.md §9.7 on a descriptor's values; the sizes only when [sizes] (on send). */
internal fun mediaProblem(media: Media, sizes: Boolean): String? = with(media) {
    textRule("title", title)?.let { return it }
    if (durationMs !in 1..Owp.MAX_DURATION_MS) return "durationMs is outside 1–${Owp.MAX_DURATION_MS} (7 days)"
    if (kind == MediaKind.EPISODE && series == null) return "an episode needs series"
    series?.let { s -> textRule("series.title", s.title)?.let { return it } }
    if ((season ?: 0) < 0 || (episode ?: 0) < 0) return "season and episode must be 0 or more"
    if ((file?.sizeBytes ?: 0) < 0) return "file.sizeBytes is negative"
    fun outOfRange(n: Long?) = n != null && n !in -Owp.MAX_INT..Owp.MAX_INT
    if (outOfRange(year) || outOfRange(series?.year) || outOfRange(season) || outOfRange(episode) || outOfRange(file?.sizeBytes)) {
        return "a number is beyond ±(2^53 − 1)"
    }
    if (file?.oshash != null && !OSHASH.matches(file.oshash)) return "file.oshash is not 16 lower-case hex digits"
    file?.name?.let { name ->
        textRule("file.name", name)?.let { return it }
        if ('/' in name || '\\' in name || name == "." || name == "..") return "file.name is a path, not a file name"
    }
    if (ids.values.any { it.isEmpty() } || series?.ids?.values?.any { it.isEmpty() } == true) return "an ID is empty"
    if (ids.holdsUnpairedSurrogate() || series?.ids?.holdsUnpairedSurrogate() == true || ext?.holdsUnpairedSurrogate() == true) {
        return "a string has an unpaired surrogate"
    }
    if (ext != null && ext.keys.any { !EXT_NAME.matches(it) }) return "an ext member's name is not a reverse-DNS name"
    if (!sizes) return null
    if (ext != null && utf8Length(ext.toString()) > Owp.MAX_EXT_BYTES) return "ext is over ${Owp.MAX_EXT_BYTES} bytes"
    if (utf8Length(toJson().toString()) > Owp.MAX_MEDIA_BYTES) return "the descriptor is over ${Owp.MAX_MEDIA_BYTES} bytes"
    return null
}

/** Null when [s] is 1 or more code points without forbidden characters or unpaired surrogates, else why not. */
private fun textRule(field: String, s: String): String? =
    if (s.isEmpty()) "$field is empty" else textProblem(s, 1, Int.MAX_VALUE)?.let { "$field $it" }

private fun Map<String, String>.holdsUnpairedSurrogate(): Boolean =
    entries.any { (name, value) -> hasUnpairedSurrogate(name) || hasUnpairedSurrogate(value) }

/** Whether a string or member name anywhere in this value holds an unpaired surrogate, which has no UTF-8 encoding. */
private fun JsonElement.holdsUnpairedSurrogate(): Boolean = when (this) {
    is JsonObject -> entries.any { (name, value) -> hasUnpairedSurrogate(name) || value.holdsUnpairedSurrogate() }
    is JsonArray -> any { it.holdsUnpairedSurrogate() }
    is JsonPrimitive -> isString && hasUnpairedSurrogate(content)
}
