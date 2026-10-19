/// The media descriptor's `kind` (SPEC.md §10).
public enum MediaKind: String, Hashable, Sendable, CaseIterable {
    case movie
    case episode
    /// A file with no catalogue identity, such as a home video, a recording or a clip.
    case video
}

/// An episode's series.
public struct Series: Hashable, Sendable {
    public var title: String
    public var year: Int64?
    /// The series' IDs, keyed by namespace.
    ///
    /// The v1 namespaces are `imdb`, `tmdb`, `tvdb`, `plex`, `anilist`, `anidb`, `kitsu` and `mal`.
    public var ids: [String: String]

    public init(title: String, year: Int64? = nil, ids: [String: String] = [:]) {
        self.title = title
        self.year = year
        self.ids = ids
    }
}

/// File hints that pick out the same release among the candidates.
public struct MediaFile: Hashable, Sendable {
    public var sizeBytes: Int64?
    /// The OpenSubtitles hash, as 16 lower-case hex digits.
    public var oshash: String?
    /// The file name, which is a hint and never a path.
    ///
    /// The name holds no directories, so no `/` or `\`, and no forbidden characters. It is not `.` or `..`.
    public var name: String?

    public init(sizeBytes: Int64? = nil, oshash: String? = nil, name: String? = nil) {
        self.sizeBytes = sizeBytes
        self.oshash = oshash
        self.name = name
    }
}

/// The media descriptor, which names the title the room plays (SPEC.md §10).
///
/// Positions are in the timeline of the reference file, whose duration is `durationMs`.
///
/// `Media` is a value type, so the party's copy never changes when the app changes its own.
public struct Media: Hashable, Sendable {
    public var kind: MediaKind
    /// The movie's or episode's title, or the video's.
    public var title: String
    /// The reference file's duration in milliseconds, from 1 to 604800000, which is 7 days.
    public var durationMs: Int64
    public var year: Int64?
    /// The title's IDs, keyed by namespace.
    ///
    /// For an episode, these are the episode's own IDs.
    public var ids: [String: String]
    /// The episode's series, which an episode requires.
    public var series: Series?
    /// The season number, as the setter's source numbers it.
    ///
    /// Specials are season 0.
    public var season: Int64?
    public var episode: Int64?
    public var file: MediaFile?
    /// App-private hints, keyed by a reverse-DNS app name such as `com.example` or `org.example`.
    ///
    /// The hints take at most 1024 bytes.
    public var ext: [String: JSONValue]?

    public init(
        kind: MediaKind,
        title: String,
        durationMs: Int64,
        year: Int64? = nil,
        ids: [String: String] = [:],
        series: Series? = nil,
        season: Int64? = nil,
        episode: Int64? = nil,
        file: MediaFile? = nil,
        ext: [String: JSONValue]? = nil
    ) {
        self.kind = kind
        self.title = title
        self.durationMs = durationMs
        self.year = year
        self.ids = ids
        self.series = series
        self.season = season
        self.episode = episode
        self.file = file
        self.ext = ext
    }

    /// The descriptor as the party sends it in `setMedia`.
    public func toJSON() -> JSONValue {
        var o: [String: JSONValue] = ["kind": .string(kind.rawValue), "title": .string(title)]
        if let year { o["year"] = .number(Double(year)) }
        if !ids.isEmpty { o["ids"] = .object(ids.mapValues(JSONValue.string)) }
        if let series {
            var s: [String: JSONValue] = ["title": .string(series.title)]
            if let year = series.year { s["year"] = .number(Double(year)) }
            if !series.ids.isEmpty { s["ids"] = .object(series.ids.mapValues(JSONValue.string)) }
            o["series"] = .object(s)
        }
        if let season { o["season"] = .number(Double(season)) }
        if let episode { o["episode"] = .number(Double(episode)) }
        o["durationMs"] = .number(Double(durationMs))
        if let file {
            var f: [String: JSONValue] = [:]
            if let size = file.sizeBytes { f["sizeBytes"] = .number(Double(size)) }
            if let oshash = file.oshash { f["oshash"] = .string(oshash) }
            if let name = file.name { f["name"] = .string(name) }
            o["file"] = .object(f)
        }
        if let ext { o["ext"] = .object(ext) }
        return .object(o)
    }

    /// The compact JSON text `setMedia` sends (SDK.md §9.7): known members only, absent ones omitted, ints integral,
    /// no whitespace, non-ASCII as UTF-8; `ids` and `ext` members by name.
    var wireText: String {
        var w = JSONObjectWriter()
        w.put("kind", kind.rawValue)
        w.put("title", title)
        if let year { w.put("year", year) }
        if !ids.isEmpty { w.put("ids", .object(ids.mapValues(JSONValue.string))) }
        if let series {
            var s = JSONObjectWriter()
            s.put("title", series.title)
            if let year = series.year { s.put("year", year) }
            if !series.ids.isEmpty { s.put("ids", .object(series.ids.mapValues(JSONValue.string))) }
            w.putRaw("series", s.done())
        }
        if let season { w.put("season", season) }
        if let episode { w.put("episode", episode) }
        w.put("durationMs", durationMs)
        if let file {
            var f = JSONObjectWriter()
            if let size = file.sizeBytes { f.put("sizeBytes", size) }
            if let oshash = file.oshash { f.put("oshash", oshash) }
            if let name = file.name { f.put("name", name) }
            w.putRaw("file", f.done())
        }
        if let ext { w.put("ext", .object(ext)) }
        return w.done()
    }

    /// Whether `other` is this descriptor as parsed JSON (SDK.md §9.1): member order ignored, strings compared code
    /// point by code point.
    func sameDescriptor(as other: Media) -> Bool { wireText.utf8.elementsEqual(other.wireText.utf8) }
}

/// Reads a descriptor with the receive checks of SDK.md §9.7.
///
/// The function reads a descriptor the way the SDK reads `media` and `joined`:
///
/// - It drops unknown members.
/// - It keeps unknown `ids` namespaces and all of `ext`.
/// - It doesn't check the sizes.
///
/// Descriptors come from other participants, so treat every string in one as untrusted text.
///
/// - Throws: `PartyError` with code `invalid_message` when `json` isn't an object, a member has the wrong type, a
///   required member is missing, or a value breaks a rule of `checkMedia` other than the sizes.
public func parseMedia(_ json: JSONValue) throws -> Media {
    guard case .object(let o) = json, let read = readMedia(o.mapValues(JSON.init)) else {
        throw PartyError(.invalidMessage, "the descriptor misses a member or has one of the wrong type")
    }
    if let problem = mediaProblem(read.media, sizes: false) { throw PartyError(.invalidMessage, problem) }
    return read.media
}

/// `parseMedia` for the SDK's own reading: nil instead of a throw, unpaired surrogates refused.
func receivedMedia(_ o: [String: JSON]) -> Media? {
    guard let read = readMedia(o), !read.unpaired, mediaProblem(read.media, sizes: false) == nil else { return nil }
    return read.media
}

/// `o` as a `Media` when every member has its type and the required ones are there; values unchecked. `unpaired`
/// says whether a string the checks read held an unpaired surrogate.
func readMedia(_ o: [String: JSON]) -> (media: Media, unpaired: Bool)? {
    guard let kind = o["kind"]?.string.flatMap(MediaKind.init(rawValue:)),
          let title = o["title"]?.text,
          let durationMs = o["durationMs"]?.int else { return nil }
    func typed(_ o: [String: JSON], _ key: String, _ check: (JSON) -> Bool) -> Bool { o[key].map(check) ?? true }
    func isStringMap(_ j: JSON) -> Bool { j.object?.values.allSatisfy { $0.string != nil } ?? false }
    let ok = typed(o, "year") { $0.int != nil }
        && typed(o, "ids", isStringMap)
        && typed(o, "series") { s in
            guard let so = s.object, so["title"]?.string != nil else { return false }
            return typed(so, "year") { $0.int != nil } && typed(so, "ids", isStringMap)
        }
        && typed(o, "season") { $0.int != nil }
        && typed(o, "episode") { $0.int != nil }
        && typed(o, "file") { f in
            guard let fo = f.object else { return false }
            return typed(fo, "sizeBytes") { $0.int != nil } && typed(fo, "oshash") { $0.string != nil }
                && typed(fo, "name") { $0.string != nil }
        }
        && typed(o, "ext") { $0.object != nil }
    guard ok else { return nil }
    var unpaired = title.unpaired
    /// A string map, and whether a name or value held an unpaired surrogate.
    func stringMap(_ j: JSON?) -> [String: String] {
        guard case .object(let members, let badName)? = j else { return [:] }
        if badName { unpaired = true }
        return members.compactMapValues { v in
            guard let t = v.text else { return nil }
            if t.unpaired { unpaired = true }
            return t.value
        }
    }
    let ids = stringMap(o["ids"])
    var series: Series?
    if let so = o["series"]?.object, let t = so["title"]?.text {
        if t.unpaired { unpaired = true }
        series = Series(title: t.value, year: so["year"]?.int, ids: stringMap(so["ids"]))
    }
    var file: MediaFile?
    if let fo = o["file"]?.object {
        let name = fo["name"]?.text
        if name?.unpaired == true { unpaired = true }
        file = MediaFile(sizeBytes: fo["sizeBytes"]?.int, oshash: fo["oshash"]?.string, name: name?.value)
    }
    var ext: [String: JSONValue]?
    if let e = o["ext"], case .object(let members, _) = e {
        if e.holdsUnpaired { unpaired = true }
        ext = members.mapValues(JSONValue.init)
    }
    let media = Media(
        kind: kind,
        title: title.value,
        durationMs: durationMs,
        year: o["year"]?.int,
        ids: ids,
        series: series,
        season: o["season"]?.int,
        episode: o["episode"]?.int,
        file: file,
        ext: ext
    )
    return (media, unpaired)
}

/// Checks a descriptor with the send checks of SDK.md §9.7.
///
/// The checks run in this order:
///
/// 1. `title`.
/// 2. `durationMs`, which is 1 to 604800000, or 7 days.
/// 3. `series`, which an episode needs.
/// 4. Number ranges.
/// 5. `oshash`.
/// 6. `file.name`, which holds no `/`, `\` or forbidden character, and is not `.` or `..`.
/// 7. IDs.
/// 8. `ext` member names and numbers.
/// 9. The sizes: at most 1024 bytes of `ext`, and 4096 bytes in all, measured on the SDK's compact JSON.
///
/// When a descriptor is too large, the app trims `file.name` or `ext` and checks again.
///
/// A Swift `String` can't hold an unpaired surrogate, so no `Media` breaks that rule.
///
/// - Returns: Nil when `media` passes the checks, or else the first rule it breaks.
public func checkMedia(_ media: Media) -> String? { mediaProblem(media, sizes: true) }

/// The rules of SDK.md §9.7 on a descriptor's values; the sizes only when `sizes` (on send).
func mediaProblem(_ media: Media, sizes: Bool) -> String? {
    if let p = textRule("title", media.title) { return p }
    if !(1...Owp.maxDurationMs).contains(media.durationMs) {
        return "durationMs is outside 1–\(Owp.maxDurationMs) (7 days)"
    }
    if media.kind == .episode && media.series == nil { return "an episode needs series" }
    if let series = media.series, let p = textRule("series.title", series.title) { return p }
    if (media.season ?? 0) < 0 || (media.episode ?? 0) < 0 { return "season and episode must be 0 or more" }
    if (media.file?.sizeBytes ?? 0) < 0 { return "file.sizeBytes is negative" }
    let numbers = [media.year, media.series?.year, media.season, media.episode, media.file?.sizeBytes]
    if numbers.contains(where: { $0.map { !Owp.intRange.contains($0) } ?? false }) {
        return "a number is beyond ±(2^53 − 1)"
    }
    if let oshash = media.file?.oshash, !isOshash(oshash) { return "file.oshash is not 16 lower-case hex digits" }
    if let name = media.file?.name {
        if let p = textRule("file.name", name) { return p }
        if name.unicodeScalars.contains(where: { $0 == "/" || $0 == "\\" }) || name == "." || name == ".." {
            return "file.name is a path, not a file name"
        }
    }
    if media.ids.values.contains(where: \.isEmpty) || media.series?.ids.values.contains(where: \.isEmpty) == true {
        return "an ID is empty"
    }
    if let ext = media.ext, ext.keys.contains(where: { !isExtName($0) }) {
        return "an ext member's name is not a reverse-DNS name"
    }
    guard sizes else { return nil }
    if let ext = media.ext {
        if ext.values.contains(where: { !$0.numbersAreFinite }) { return "ext holds a number JSON can't carry" }
        if utf8Length(JSONValue.object(ext).description) > Owp.maxExtBytes { return "ext is over \(Owp.maxExtBytes) bytes" }
    }
    if utf8Length(media.wireText) > Owp.maxMediaBytes { return "the descriptor is over \(Owp.maxMediaBytes) bytes" }
    return nil
}

/// `^[a-z0-9-]+(\.[a-z0-9-]+)+$`: two or more dot-separated labels of `a`–`z`, `0`–`9` and `-`.
private func isExtName(_ name: String) -> Bool {
    let labels = name.unicodeScalars.split(separator: ".", omittingEmptySubsequences: false)
    return labels.count >= 2 && labels.allSatisfy { label in
        !label.isEmpty && label.allSatisfy { (0x61...0x7A).contains($0.value) || (0x30...0x39).contains($0.value) || $0 == "-" }
    }
}

/// Nil when `s` is 1 or more code points without forbidden characters, else why not.
private func textRule(_ field: String, _ s: String) -> String? {
    if s.isEmpty { return "\(field) is empty" }
    return textProblem(s, min: 1, max: Int.max).map { "\(field) \($0)" }
}

private func isOshash(_ s: String) -> Bool {
    s.unicodeScalars.count == 16 && s.unicodeScalars.allSatisfy { (0x30...0x39).contains($0.value) || (0x61...0x66).contains($0.value) }
}

extension JSONValue {
    /// Whether every number in this value is finite: JSON has no form for the others.
    var numbersAreFinite: Bool {
        switch self {
        case .null, .bool, .string: return true
        case .number(let d): return d.isFinite
        case .array(let items): return items.allSatisfy(\.numbersAreFinite)
        case .object(let members): return members.values.allSatisfy(\.numbersAreFinite)
        }
    }
}
