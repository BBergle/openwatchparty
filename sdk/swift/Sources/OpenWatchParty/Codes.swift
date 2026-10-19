/// Separators dropped from typed or pasted codes (SDK.md §4.1).
private let codeSeparators: Set<UInt32> = [
    0x20, 0x09, 0x0A, 0x0D, 0xA0, 0x2007, 0x202F,
    0x2D, 0x2010, 0x2011, 0x2012, 0x2013, 0x2014, 0x2212,
]

private let alphabet = Set(Owp.alphabet.unicodeScalars)

/// The code as the relay knows it: separators removed, ASCII letters upper-cased, exactly 6 alphabet symbols; nil when
/// `input` isn't a code (SDK.md §4.1).
func normalizeCode(_ input: String) -> String? {
    var out = String.UnicodeScalarView()
    for c in input.unicodeScalars {
        if codeSeparators.contains(c.value) { continue }
        let u = (0x61...0x7A).contains(c.value) ? Unicode.Scalar(c.value - 0x20)! : c
        guard alphabet.contains(u) else { return nil }
        out.append(u)
        if out.count > Owp.codeLength { return nil }
    }
    return out.count == Owp.codeLength ? String(out) : nil
}

/// `code`, as typed or pasted, normalized and shown in two groups: `K7M-Q2X` (SDK.md §4.1).
///
/// - Throws: `PartyError` `invalid_code` when `code` isn't a room code.
public func formatCode(_ code: String) throws -> String {
    guard let c = normalizeCode(code) else { throw PartyError(.invalidCode, "not a room code") }
    return String(c.prefix(3)) + "-" + String(c.suffix(3))
}

/// The relay of `relays` that owns `code`'s first symbol, or nil (SDK.md §4.2). `code` is normalized.
func route(_ code: String, _ relays: [Relay]) -> Relay? {
    guard let first = code.unicodeScalars.first else { return nil }
    return relays.first { $0.prefixes.unicodeScalars.contains(first) }
}

/// A room code as the relay sends it: 6 alphabet symbols.
func isRoomCode(_ s: String) -> Bool {
    s.unicodeScalars.count == Owp.codeLength && s.unicodeScalars.allSatisfy { alphabet.contains($0) }
}

/// A peer ID as SPEC.md §11.1 shapes it: `^[A-Za-z0-9_-]{1,16}$`.
func isPeerId(_ s: String) -> Bool { isBase64Url(s, min: 1, max: 16) }

/// A resume token as SPEC.md §11.1 shapes it: 43 base64url characters.
func isToken(_ s: String) -> Bool { isBase64Url(s, min: 43, max: 43) }

private func isBase64Url(_ s: String, min: Int, max: Int) -> Bool {
    let scalars = s.unicodeScalars
    guard (min...max).contains(scalars.count) else { return false }
    return scalars.allSatisfy { c in
        switch c.value {
        case 0x41...0x5A, 0x61...0x7A, 0x30...0x39, 0x5F, 0x2D: return true
        default: return false
        }
    }
}
