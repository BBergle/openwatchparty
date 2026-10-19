package party.openwatch

/** A forbidden character (SPEC.md §2): general category Cc or a Bidi_Control character. All of them are in the BMP. */
internal fun forbidden(c: Char): Boolean {
    val code = c.code
    return code <= 0x1f || code in 0x7f..0x9f || code == 0x061c || code == 0x200e || code == 0x200f ||
        code in 0x202a..0x202e || code in 0x2066..0x2069
}

/** Whether [s] holds a UTF-16 surrogate that isn't half of a pair. Such a string has no valid UTF-8 encoding. */
internal fun hasUnpairedSurrogate(s: String): Boolean {
    var i = 0
    while (i < s.length) {
        val c = s[i]
        if (c.isHighSurrogate() && i + 1 < s.length && s[i + 1].isLowSurrogate()) {
            i += 2
            continue
        }
        if (c.isSurrogate()) return true
        i++
    }
    return false
}

/**
 * Null when [s] has [min] to [max] code points, no forbidden character and no unpaired surrogate (SPEC.md §2, §6.3);
 * else why not.
 */
internal fun textProblem(s: String, min: Int, max: Int): String? {
    if (hasUnpairedSurrogate(s)) return "has an unpaired surrogate"
    s.firstOrNull(::forbidden)?.let { return "has the forbidden character U+${it.code.toString(16).uppercase().padStart(4, '0')}" }
    val points = s.length - s.count { it.isLowSurrogate() }
    return if (points in min..max) null else "must be $min–$max characters"
}

/** Null when [name] is a valid display name (SPEC.md §6.3), else why not. */
internal fun nameProblem(name: String): String? = textProblem(name, 1, Owp.MAX_NAME_LENGTH)

/** Null when [client] is a valid `<app>/<version>` (SPEC.md §6.3), else why not. */
internal fun clientProblem(client: String): String? {
    textProblem(client, 1, Owp.MAX_CLIENT_LENGTH)?.let { return it }
    val slash = client.indexOf('/')
    return if (slash <= 0 || slash == client.length - 1) "must be <app>/<version>" else null
}
