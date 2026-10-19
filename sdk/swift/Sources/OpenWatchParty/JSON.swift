/// A JSON value: what `Media.ext` holds (SDK.md §9.7) and what `parseMedia` reads. `description` is its compact JSON
/// text, members sorted by name.
public enum JSONValue: Hashable, Sendable {
    case null
    case bool(Bool)
    /// Any JSON number. Integers beyond ±(2^53 − 1) lose precision, as in JavaScript.
    case number(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    /// Parses JSON text.
    ///
    /// - Throws: `PartyError` `invalid_message` when `text` isn't JSON, or holds an unpaired surrogate escape
    ///   (`"\ud800"`), which a Swift `String` can't hold.
    public init(parsing text: String) throws {
        guard let json = JSON.parse(text) else { throw PartyError(.invalidMessage, "not JSON text") }
        if json.holdsUnpaired { throw PartyError(.invalidMessage, "the text holds an unpaired surrogate") }
        self.init(json)
    }

    /// The SDK's reading of `json`; unpaired surrogates were replaced by U+FFFD.
    init(_ json: JSON) {
        switch json {
        case .null: self = .null
        case .bool(let b): self = .bool(b)
        case .number(let d): self = .number(d)
        case .string(let s, _): self = .string(s)
        case .array(let items): self = .array(items.map(JSONValue.init))
        case .object(let members, _): self = .object(members.mapValues(JSONValue.init))
        }
    }
}

extension JSONValue: CustomStringConvertible {
    public var description: String {
        var out = ""
        JSONWriter.write(self, into: &out)
        return out
    }
}

extension JSONValue: ExpressibleByNilLiteral, ExpressibleByBooleanLiteral, ExpressibleByIntegerLiteral,
    ExpressibleByFloatLiteral, ExpressibleByStringLiteral, ExpressibleByArrayLiteral, ExpressibleByDictionaryLiteral
{
    public init(nilLiteral: ()) { self = .null }
    public init(booleanLiteral value: Bool) { self = .bool(value) }
    public init(integerLiteral value: Int) { self = .number(Double(value)) }
    public init(floatLiteral value: Double) { self = .number(value) }
    public init(stringLiteral value: String) { self = .string(value) }
    public init(arrayLiteral elements: JSONValue...) { self = .array(elements) }

    /// A repeated name keeps its last value.
    public init(dictionaryLiteral elements: (String, JSONValue)...) {
        self = .object(Dictionary(elements, uniquingKeysWith: { _, last in last }))
    }
}

/// JSON as the SDK reads it from the relay, the vectors and stored lists. A Swift `String` can't hold an unpaired
/// surrogate, so the reader replaces each unpaired surrogate escape with U+FFFD and marks the string (or, for a
/// member name, the object) so the checks that refuse unpaired surrogates still see them (SDK.md §7.1, §9.7).
indirect enum JSON: Sendable {
    case null
    case bool(Bool)
    case number(Double)
    /// `unpaired`: the text held an unpaired surrogate escape.
    case string(String, unpaired: Bool)
    case array([JSON])
    /// `unpaired`: a member name held an unpaired surrogate escape. A repeated name keeps its last value.
    case object([String: JSON], unpaired: Bool)

    init(_ value: JSONValue) {
        switch value {
        case .null: self = .null
        case .bool(let b): self = .bool(b)
        case .number(let d): self = .number(d)
        case .string(let s): self = .string(s, unpaired: false)
        case .array(let items): self = .array(items.map(JSON.init))
        case .object(let members): self = .object(members.mapValues(JSON.init), unpaired: false)
        }
    }

    /// A JSON number with an integral value within ±(2^53 − 1), as SPEC.md §2 reads an int: `3.0` and `3e0` are 3.
    var int: Int64? {
        guard case .number(let d) = self, d.isFinite, d == d.rounded(.towardZero),
              d.magnitude <= Double(Owp.maxInt) else { return nil }
        return Int64(d)
    }

    var number: Double? {
        if case .number(let d) = self { return d }
        return nil
    }

    /// The string, unpaired surrogates replaced by U+FFFD; use `text` where they must be refused.
    var string: String? {
        if case .string(let s, _) = self { return s }
        return nil
    }

    /// The string and whether it held an unpaired surrogate.
    var text: (value: String, unpaired: Bool)? {
        if case .string(let s, let unpaired) = self { return (s, unpaired) }
        return nil
    }

    var bool: Bool? {
        if case .bool(let b) = self { return b }
        return nil
    }

    var object: [String: JSON]? {
        if case .object(let members, _) = self { return members }
        return nil
    }

    var array: [JSON]? {
        if case .array(let items) = self { return items }
        return nil
    }

    var isNull: Bool {
        if case .null = self { return true }
        return false
    }

    subscript(key: String) -> JSON? { object?[key] }

    /// Whether a string or member name anywhere in this value held an unpaired surrogate.
    var holdsUnpaired: Bool {
        switch self {
        case .null, .bool, .number: return false
        case .string(_, let unpaired): return unpaired
        case .array(let items): return items.contains { $0.holdsUnpaired }
        case .object(let members, let unpaired): return unpaired || members.values.contains { $0.holdsUnpaired }
        }
    }

    /// Nesting deeper than this is refused: a descriptor's `ext` reaches about 510 levels within the relay's 1024
    /// bytes, so no message of a relay that keeps SPEC.md's limits comes near it.
    static let maxDepth = 1024

    /// Parses JSON text (RFC 8259); nil when it isn't JSON.
    static func parse(_ text: String) -> JSON? {
        var reader = JSONReader(Array(text.utf8))
        return reader.document()
    }
}

/// A non-recursive reader of JSON text.
private struct JSONReader {
    private let bytes: [UInt8]
    private var i = 0

    init(_ bytes: [UInt8]) { self.bytes = bytes }

    private enum Frame {
        case array([JSON])
        case object([String: JSON], unpaired: Bool, key: String)
    }

    mutating func document() -> JSON? {
        var stack: [Frame] = []
        while true {
            skipSpace()
            guard i < bytes.count else { return nil }
            var value: JSON
            switch bytes[i] {
            case UInt8(ascii: "{"):
                i += 1
                skipSpace()
                if peek(UInt8(ascii: "}")) {
                    i += 1
                    value = .object([:], unpaired: false)
                } else {
                    guard stack.count < JSON.maxDepth, let (key, unpaired) = memberName() else { return nil }
                    stack.append(.object([:], unpaired: unpaired, key: key))
                    continue
                }
            case UInt8(ascii: "["):
                i += 1
                skipSpace()
                if peek(UInt8(ascii: "]")) {
                    i += 1
                    value = .array([])
                } else {
                    guard stack.count < JSON.maxDepth else { return nil }
                    stack.append(.array([]))
                    continue
                }
            case UInt8(ascii: "\""):
                guard let (s, unpaired) = string() else { return nil }
                value = .string(s, unpaired: unpaired)
            case UInt8(ascii: "t"):
                guard literal("true") else { return nil }
                value = .bool(true)
            case UInt8(ascii: "f"):
                guard literal("false") else { return nil }
                value = .bool(false)
            case UInt8(ascii: "n"):
                guard literal("null") else { return nil }
                value = .null
            default:
                guard let d = number() else { return nil }
                value = .number(d)
            }
            // Hand the value to the containers it closes.
            while true {
                guard let top = stack.popLast() else {
                    skipSpace()
                    return i == bytes.count ? value : nil
                }
                skipSpace()
                guard i < bytes.count else { return nil }
                switch top {
                case .array(var items):
                    items.append(value)
                    if bytes[i] == UInt8(ascii: ",") {
                        i += 1
                        stack.append(.array(items))
                    } else if bytes[i] == UInt8(ascii: "]") {
                        i += 1
                        value = .array(items)
                        continue
                    } else {
                        return nil
                    }
                case .object(var members, let unpaired, let key):
                    members[key] = value
                    if bytes[i] == UInt8(ascii: ",") {
                        i += 1
                        skipSpace()
                        guard let (next, nextUnpaired) = memberName() else { return nil }
                        stack.append(.object(members, unpaired: unpaired || nextUnpaired, key: next))
                    } else if bytes[i] == UInt8(ascii: "}") {
                        i += 1
                        value = .object(members, unpaired: unpaired)
                        continue
                    } else {
                        return nil
                    }
                }
                break
            }
        }
    }

    private func peek(_ b: UInt8) -> Bool { i < bytes.count && bytes[i] == b }

    private mutating func skipSpace() {
        while i < bytes.count, bytes[i] == 0x20 || bytes[i] == 0x09 || bytes[i] == 0x0A || bytes[i] == 0x0D { i += 1 }
    }

    /// `"name"` then `:`.
    private mutating func memberName() -> (String, Bool)? {
        guard peek(UInt8(ascii: "\"")), let name = string() else { return nil }
        skipSpace()
        guard peek(UInt8(ascii: ":")) else { return nil }
        i += 1
        return name
    }

    private mutating func literal(_ word: StaticString) -> Bool {
        let count = word.utf8CodeUnitCount
        guard i + count <= bytes.count else { return false }
        let ok = word.withUTF8Buffer { w in (0..<count).allSatisfy { bytes[i + $0] == w[$0] } }
        if ok { i += count }
        return ok
    }

    private mutating func number() -> Double? {
        let start = i
        func digits() -> Int {
            let from = i
            while i < bytes.count, bytes[i] >= 0x30, bytes[i] <= 0x39 { i += 1 }
            return i - from
        }
        if peek(UInt8(ascii: "-")) { i += 1 }
        if peek(UInt8(ascii: "0")) {
            i += 1
        } else if digits() == 0 {
            return nil
        }
        if peek(UInt8(ascii: ".")) {
            i += 1
            if digits() == 0 { return nil }
        }
        if peek(UInt8(ascii: "e")) || peek(UInt8(ascii: "E")) {
            i += 1
            if peek(UInt8(ascii: "+")) || peek(UInt8(ascii: "-")) { i += 1 }
            if digits() == 0 { return nil }
        }
        return Double(String(decoding: bytes[start..<i], as: UTF8.self))
    }

    /// A string literal at `i`; the flag is set when it held an unpaired surrogate escape.
    private mutating func string() -> (String, Bool)? {
        i += 1
        var out: [UInt8] = []
        var unpaired = false
        while i < bytes.count {
            let b = bytes[i]
            switch b {
            case UInt8(ascii: "\""):
                i += 1
                return (String(decoding: out, as: UTF8.self), unpaired)
            case UInt8(ascii: "\\"):
                i += 1
                guard i < bytes.count else { return nil }
                let e = bytes[i]
                i += 1
                switch e {
                case UInt8(ascii: "\""), UInt8(ascii: "\\"), UInt8(ascii: "/"): out.append(e)
                case UInt8(ascii: "b"): out.append(0x08)
                case UInt8(ascii: "f"): out.append(0x0C)
                case UInt8(ascii: "n"): out.append(0x0A)
                case UInt8(ascii: "r"): out.append(0x0D)
                case UInt8(ascii: "t"): out.append(0x09)
                case UInt8(ascii: "u"):
                    guard let unit = hex4() else { return nil }
                    var scalar: UInt32?
                    if unit >= 0xD800 && unit <= 0xDBFF {
                        // A high surrogate counts only directly followed by a low surrogate escape.
                        if i + 1 < bytes.count, bytes[i] == UInt8(ascii: "\\"), bytes[i + 1] == UInt8(ascii: "u") {
                            let save = i
                            i += 2
                            if let low = hex4(), low >= 0xDC00 && low <= 0xDFFF {
                                scalar = 0x10000 + ((unit - 0xD800) << 10) + (low - 0xDC00)
                            } else {
                                i = save
                            }
                        }
                    } else if unit < 0xDC00 || unit > 0xDFFF {
                        scalar = unit
                    }
                    if let scalar, let s = Unicode.Scalar(scalar) {
                        UTF8.encode(s) { out.append($0) }
                    } else {
                        unpaired = true
                        out.append(contentsOf: [0xEF, 0xBF, 0xBD])
                    }
                default:
                    return nil
                }
            default:
                if b < 0x20 { return nil }
                out.append(b)
                i += 1
            }
        }
        return nil
    }

    private mutating func hex4() -> UInt32? {
        guard i + 4 <= bytes.count else { return nil }
        var v: UInt32 = 0
        for k in 0..<4 {
            let b = bytes[i + k]
            let d: UInt8
            switch b {
            case 0x30...0x39: d = b - 0x30
            case 0x41...0x46: d = b - 0x41 + 10
            case 0x61...0x66: d = b - 0x61 + 10
            default: return nil
            }
            v = v << 4 | UInt32(d)
        }
        i += 4
        return v
    }
}

/// Compact JSON text, as the SDK sends it: no whitespace, non-ASCII as UTF-8, ints without fraction or exponent.
enum JSONWriter {
    /// `value` with object members sorted by name.
    static func write(_ value: JSONValue, into out: inout String) {
        switch value {
        case .null: out += "null"
        case .bool(let b): out += b ? "true" : "false"
        case .number(let d): number(d, into: &out)
        case .string(let s): string(s, into: &out)
        case .array(let items):
            out += "["
            for (k, item) in items.enumerated() {
                if k > 0 { out += "," }
                write(item, into: &out)
            }
            out += "]"
        case .object(let members):
            out += "{"
            for (k, name) in members.keys.sorted(by: utf8Less).enumerated() {
                if k > 0 { out += "," }
                string(name, into: &out)
                out += ":"
                write(members[name]!, into: &out)
            }
            out += "}"
        }
    }

    /// An integral value within ±(2^53 − 1) as an int (`1`, not `1.0`); others in Swift's shortest form. A number
    /// that isn't finite has no JSON form: `checkMedia` refuses one, so it never goes out.
    static func number(_ d: Double, into out: inout String) {
        if d.isNaN {
            out += "NaN"
        } else if d.isInfinite {
            out += d > 0 ? "1e999" : "-1e999"
        } else if d == d.rounded(.towardZero), d.magnitude <= Double(Owp.maxInt) {
            out += String(Int64(d))
        } else {
            out += "\(d)"
        }
    }

    static func string(_ s: String, into out: inout String) {
        out += "\""
        for scalar in s.unicodeScalars {
            switch scalar {
            case "\"": out += "\\\""
            case "\\": out += "\\\\"
            case "\n": out += "\\n"
            case "\r": out += "\\r"
            case "\t": out += "\\t"
            case "\u{08}": out += "\\b"
            case "\u{0C}": out += "\\f"
            default:
                if scalar.value < 0x20 {
                    let hex = String(scalar.value, radix: 16)
                    out += "\\u" + String(repeating: "0", count: 4 - hex.count) + hex
                } else {
                    out.unicodeScalars.append(scalar)
                }
            }
        }
        out += "\""
    }

    /// Code-unit order of UTF-8, the same on every platform.
    static func utf8Less(_ a: String, _ b: String) -> Bool { a.utf8.lexicographicallyPrecedes(b.utf8) }
}

/// An object written member by member, in the order given (SPEC.md §4: never a repeated name).
struct JSONObjectWriter {
    private(set) var text = "{"
    private var first = true

    private mutating func name(_ name: String) {
        if !first { text += "," }
        first = false
        JSONWriter.string(name, into: &text)
        text += ":"
    }

    mutating func put(_ name: String, _ value: String) {
        self.name(name)
        JSONWriter.string(value, into: &text)
    }

    mutating func put(_ name: String, _ value: Int64) {
        self.name(name)
        text += String(value)
    }

    mutating func put(_ name: String, _ value: Double) {
        self.name(name)
        JSONWriter.number(value, into: &text)
    }

    mutating func put(_ name: String, _ value: Bool) {
        self.name(name)
        text += value ? "true" : "false"
    }

    mutating func putNull(_ name: String) {
        self.name(name)
        text += "null"
    }

    /// `json`, already JSON text.
    mutating func putRaw(_ name: String, _ json: String) {
        self.name(name)
        text += json
    }

    mutating func put(_ name: String, _ value: JSONValue) {
        self.name(name)
        JSONWriter.write(value, into: &text)
    }

    func done() -> String { text + "}" }
}

/// Exact equality of two strings, code point by code point. Swift's `==` treats canonically equivalent strings
/// (`"é"` and `"e\u{301}"`) as equal; the protocol's comparisons don't (SDK.md §2).
func exactlyEqual(_ a: String, _ b: String) -> Bool { a.unicodeScalars.elementsEqual(b.unicodeScalars) }

/// UTF-8 byte count of `text`.
func utf8Length(_ text: String) -> Int { text.utf8.count }
