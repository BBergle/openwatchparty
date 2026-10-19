package party.openwatch

/** Separators dropped from typed or pasted codes (SDK.md §4.1). */
private val CODE_SEPARATORS: Set<Char> = setOf(
    ' ', '\t', '\n', '\r', '\u00a0', '\u2007', '\u202f',
    '-', '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2212',
)

/**
 * The code as the relay knows it: separators removed, ASCII letters upper-cased, exactly 6 alphabet symbols; null when
 * [input] isn't a code (SDK.md §4.1).
 */
internal fun normalizeCode(input: String): String? {
    val out = StringBuilder(Owp.CODE_LENGTH)
    for (c in input) {
        if (c in CODE_SEPARATORS) continue
        val u = if (c in 'a'..'z') c - ('a' - 'A') else c
        if (Owp.ALPHABET.indexOf(u) < 0) return null
        out.append(u)
    }
    return if (out.length == Owp.CODE_LENGTH) out.toString() else null
}

/**
 * Formats a room code for display in two groups, such as `K7M-Q2X` (SDK.md §4.1).
 *
 * [code] may be typed or pasted. The function normalizes it first.
 *
 * @throws PartyException `invalid_code` when [code] isn't a room code.
 */
public fun formatCode(code: String): String {
    val c = normalizeCode(code) ?: throw PartyException(PartyException.INVALID_CODE, "not a room code")
    return c.substring(0, 3) + "-" + c.substring(3)
}

/** The relay of [relays] that owns [code]'s first symbol, or null (SDK.md §4.2). [code] is normalized. */
internal fun route(code: String, relays: List<Relay>): Relay? = relays.firstOrNull { code[0] in it.prefixes }
