/// Whether two descriptors name the same title (SDK.md §9.3).
///
/// Two descriptors name the same title only when their kinds are equal. Then the rule depends on the kind:
///
/// - Two movies match when they share a v1 ID.
/// - Two episodes match when they share a v1 ID of their own. Failing that, they match when they share a series ID and
///   have equal season and episode numbers, unless a v1 namespace holds two different IDs of their own.
/// - Two videos match when they are the same file.
///
/// The rule is symmetric, so the order of `a` and `b` doesn't matter.
public func sameTitle(_ a: Media, _ b: Media) -> Bool {
    guard a.kind == b.kind else { return false }
    switch a.kind {
    case .movie:
        return sharesId(a.ids, b.ids)
    case .episode:
        if sharesId(a.ids, b.ids) { return true }
        // Differing episode IDs beat equal numbers: the two sides may number the series differently.
        guard !differsId(a.ids, b.ids), let sa = a.series, let sb = b.series, sharesId(sa.ids, sb.ids) else {
            return false
        }
        return a.season != nil && a.season == b.season && a.episode != nil && a.episode == b.episode
    case .video:
        return sameFile(a.file, b.file)
    }
}

private func sharesId(_ a: [String: String], _ b: [String: String]) -> Bool {
    Owp.namespaces.contains { ns in
        guard let x = a[ns], !x.isEmpty, let y = b[ns] else { return false }
        return exactlyEqual(x, y)
    }
}

private func differsId(_ a: [String: String], _ b: [String: String]) -> Bool {
    Owp.namespaces.contains { ns in
        guard let x = a[ns], !x.isEmpty, let y = b[ns], !y.isEmpty else { return false }
        return !exactlyEqual(x, y)
    }
}

/// Equal and non-empty after mapping ASCII upper case to lower case only.
private func foldEqual(_ a: String?, _ b: String?) -> Bool {
    guard let a, let b, !a.isEmpty, !b.isEmpty else { return false }
    return a.unicodeScalars.map(asciiLower).elementsEqual(b.unicodeScalars.map(asciiLower))
}

private func asciiLower(_ c: Unicode.Scalar) -> UInt32 { (0x41...0x5A).contains(c.value) ? c.value + 0x20 : c.value }

private func sameSize(_ a: Int64?, _ b: Int64?) -> Bool { a != nil && a == b }

/// The `video` identity rule: equal oshash, or equal size and name.
private func sameFile(_ a: MediaFile?, _ b: MediaFile?) -> Bool {
    guard let a, let b else { return false }
    return foldEqual(a.oshash, b.oshash) || (sameSize(a.sizeBytes, b.sizeBytes) && foldEqual(a.name, b.name))
}

/// A candidate duration that counts (SDK.md §9.2): 1 to 604800000 ms. Any other value is no duration.
func countedDuration(_ ms: Int64?) -> Int64? {
    guard let ms, (1...Owp.maxDurationMs).contains(ms) else { return nil }
    return ms
}

/// The candidates the walk takes, in its order (SDK.md §9.4): every candidate, for every kind, sorted stably; the
/// resolver decided which files are this title.
func rank<Ref>(_ media: Media, _ candidates: [Candidate<Ref>]) -> [Candidate<Ref>] {
    let file = media.file ?? MediaFile()
    func bit(_ b: Bool) -> Int { b ? 0 : 1 }
    func key(_ c: Candidate<Ref>) -> [Int64] {
        let counted = countedDuration(c.durationMs)
        return [
            Int64(bit(foldEqual(c.oshash, file.oshash))),
            Int64(bit(sameSize(c.sizeBytes, file.sizeBytes))),
            Int64(bit(foldEqual(c.name, file.name))),
            Int64(bit(counted != nil)),
            counted.map { abs($0 - media.durationMs) } ?? 0,
        ]
    }
    return candidates.enumerated()
        .map { (index: $0.offset, key: key($0.element), candidate: $0.element) }
        .sorted { a, b in a.key == b.key ? a.index < b.index : a.key.lexicographicallyPrecedes(b.key) }
        .map(\.candidate)
}

enum WalkOutcome<Ref> {
    case matched(Candidate<Ref>, deltaMs: Int64)
    /// No candidate within the tolerance; the candidate is the closest.
    case mismatch(Candidate<Ref>, deltaMs: Int64)
    case unavailable
}

struct WalkResult<Ref> {
    let ranked: [Candidate<Ref>]
    /// The candidates whose probe finished within its deadline.
    let probed: [String]
    let outcome: WalkOutcome<Ref>
}

/// The matching walk (SDK.md §9.5) over the ranked `candidates`. Probes run one at a time, at most 4, with a shared
/// budget of 60 s measured on `clock`; `progress` gets every ranked candidate with its delta once known.
///
/// - Throws: `CancellationError` when the task running it is cancelled.
@MainActor
func walk<Ref>(
    media: Media,
    candidates: [Candidate<Ref>],
    clock: PartyClock,
    progress: ([Ranked<Ref>]) -> Void = { _ in }
) async throws -> WalkResult<Ref> {
    let ranked = rank(media, candidates)
    // A duration the app gave is known from the start, also for candidates the walk never reaches.
    var deltas = ranked.map { c in countedDuration(c.durationMs).map { $0 - media.durationMs } }
    func publish() { progress(zip(ranked, deltas).map { Ranked(candidate: $0, deltaMs: $1) }) }
    publish()
    var probed: [String] = []
    var probes = 0
    var spent: Int64 = 0
    var budgetGone = false
    var closest: Int?
    for (i, c) in ranked.enumerated() {
        let known = countedDuration(c.durationMs)
        let duration: Int64?
        if let known {
            duration = known
        } else if let probe = c.probe {
            if probes == Owp.maxProbes || budgetGone { continue }
            let left = Owp.probeBudgetMs - spent
            let started = clock.nowMs()
            // One extra millisecond so a probe that ends exactly at its deadline still counts.
            let outcome = await clock.race(timeoutMs: left + 1) { try await probe() }
            try Task.checkCancellation()
            spent += clock.nowMs() - started
            var answer: Int64?
            switch outcome {
            case .timedOut:
                budgetGone = true
                continue
            case .value(let d):
                answer = d
            case .failure:
                // A probe that throws returned nothing, also when it throws a cancellation of its own (SDK.md §9.2).
                answer = nil
            }
            if spent > Owp.probeBudgetMs {
                budgetGone = true
                continue
            }
            probes += 1
            probed.append(c.id)
            duration = countedDuration(answer)
        } else {
            duration = nil
        }
        guard let duration else { continue }
        let delta = duration - media.durationMs
        if known == nil {
            deltas[i] = delta
            publish()
        }
        if abs(delta) <= Owp.matchToleranceMs {
            return WalkResult(ranked: ranked, probed: probed, outcome: .matched(c, deltaMs: delta))
        }
        if closest == nil || abs(delta) < abs(deltas[closest!]!) { closest = i }
    }
    let outcome: WalkOutcome<Ref> = closest.map { .mismatch(ranked[$0], deltaMs: deltas[$0]!) } ?? .unavailable
    return WalkResult(ranked: ranked, probed: probed, outcome: outcome)
}
