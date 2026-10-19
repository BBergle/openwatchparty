/// A forbidden character (SPEC.md §2): general category Cc or a Bidi_Control character.
func isForbidden(_ scalar: Unicode.Scalar) -> Bool {
    let v = scalar.value
    return v <= 0x1F || (0x7F...0x9F).contains(v) || v == 0x061C || v == 0x200E || v == 0x200F
        || (0x202A...0x202E).contains(v) || (0x2066...0x2069).contains(v)
}

/// Nil when `s` has `min` to `max` code points, no forbidden character and no unpaired surrogate (SPEC.md §2, §6.3);
/// else why not. `unpaired` says whether the JSON text `s` came from held an unpaired surrogate escape: a Swift
/// `String` can't hold one, so the reader marks it instead (see `JSON`).
func textProblem(_ s: String, min: Int, max: Int, unpaired: Bool = false) -> String? {
    if unpaired { return "has an unpaired surrogate" }
    if let c = s.unicodeScalars.first(where: isForbidden) {
        let hex = String(c.value, radix: 16, uppercase: true)
        return "has the forbidden character U+" + String(repeating: "0", count: Swift.max(0, 4 - hex.count)) + hex
    }
    let points = s.unicodeScalars.count
    return (min...max).contains(points) ? nil : "must be \(min)–\(max) characters"
}

/// Nil when `name` is a valid display name (SPEC.md §6.3), else why not.
func nameProblem(_ name: String, unpaired: Bool = false) -> String? {
    textProblem(name, min: 1, max: Owp.maxNameLength, unpaired: unpaired)
}

/// Nil when `client` is a valid `<app>/<version>` (SPEC.md §6.3), else why not.
func clientProblem(_ client: String, unpaired: Bool = false) -> String? {
    if let problem = textProblem(client, min: 1, max: Owp.maxClientLength, unpaired: unpaired) { return problem }
    let scalars = Array(client.unicodeScalars)
    guard let slash = scalars.firstIndex(of: "/"), slash > 0, slash < scalars.count - 1 else {
        return "must be <app>/<version>"
    }
    return nil
}
