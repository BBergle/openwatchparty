package party.openwatch

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withTimeoutOrNull
import kotlin.math.abs

/**
 * Whether two descriptors name the same title (SDK.md §9.3): equal kinds and a shared v1 ID; for an episode its own
 * ID, or, when no v1 namespace holds two different own IDs, a shared series ID with equal season and episode; for a
 * video the same file.
 */
public fun sameTitle(a: Media, b: Media): Boolean {
    if (a.kind != b.kind) return false
    return when (a.kind) {
        MediaKind.MOVIE -> sharesId(a.ids, b.ids)
        // Differing episode IDs beat equal numbers: the two sides may number the series differently.
        MediaKind.EPISODE -> sharesId(a.ids, b.ids) || (
            !differsId(a.ids, b.ids) && a.series != null && b.series != null && sharesId(a.series.ids, b.series.ids) &&
                a.season != null && a.season == b.season && a.episode != null && a.episode == b.episode
            )
        MediaKind.VIDEO -> sameFile(a.file, b.file)
    }
}

private fun sharesId(a: Map<String, String>, b: Map<String, String>): Boolean =
    Owp.NAMESPACES.any { ns -> a[ns].let { !it.isNullOrEmpty() && it == b[ns] } }

private fun differsId(a: Map<String, String>, b: Map<String, String>): Boolean =
    Owp.NAMESPACES.any { ns -> !a[ns].isNullOrEmpty() && !b[ns].isNullOrEmpty() && a[ns] != b[ns] }

/** Equal and non-empty after mapping ASCII upper case to lower case only. */
private fun foldEqual(a: String?, b: String?): Boolean =
    !a.isNullOrEmpty() && !b.isNullOrEmpty() && a.length == b.length && a.indices.all { asciiLower(a[it]) == asciiLower(b[it]) }

private fun asciiLower(c: Char): Char = if (c in 'A'..'Z') c + ('a' - 'A') else c

private fun sameSize(a: Long?, b: Long?): Boolean = a != null && a == b

/** The `video` identity rule: equal oshash, or equal size and name. */
private fun sameFile(a: MediaFile?, b: MediaFile?): Boolean {
    if (a == null || b == null) return false
    return foldEqual(a.oshash, b.oshash) || (sameSize(a.sizeBytes, b.sizeBytes) && foldEqual(a.name, b.name))
}

/** A candidate duration that counts (SDK.md §9.2): 1 to 604800000 ms. Any other value is no duration. */
internal fun countedDuration(ms: Long?): Long? = ms?.takeIf { it in 1..Owp.MAX_DURATION_MS }

/**
 * The candidates the walk takes, in its order (SDK.md §9.4): every candidate, for every kind; the resolver decided
 * which files are this title.
 */
internal fun <T> rank(media: Media, candidates: List<Candidate<T>>): List<Candidate<T>> {
    val file = media.file ?: MediaFile()
    fun bit(b: Boolean) = if (b) 0 else 1
    return candidates.sortedWith(
        compareBy<Candidate<T>> { bit(foldEqual(it.oshash, file.oshash)) }
            .thenBy { bit(sameSize(it.sizeBytes, file.sizeBytes)) }
            .thenBy { bit(foldEqual(it.name, file.name)) }
            .thenBy { bit(countedDuration(it.durationMs) != null) }
            .thenBy { c -> countedDuration(c.durationMs)?.let { abs(it - media.durationMs) } ?: 0L },
    )
}

internal sealed interface WalkOutcome<out T> {
    data class Matched<T>(val candidate: Candidate<T>, val deltaMs: Long) : WalkOutcome<T>

    /** No candidate within the tolerance; [candidate] is the closest. */
    data class Mismatch<T>(val candidate: Candidate<T>, val deltaMs: Long) : WalkOutcome<T>

    data object Unavailable : WalkOutcome<Nothing>
}

internal class WalkResult<T>(
    val ranked: List<Candidate<T>>,
    /** The candidates whose probe finished within its deadline. */
    val probed: List<String>,
    val outcome: WalkOutcome<T>,
)

/**
 * The matching walk (SDK.md §9.5) over the ranked [candidates]. Probes run one at a time, at most 4, with a shared
 * budget of 60 s measured on [clock]; [progress] gets every ranked candidate with its delta once known.
 */
internal suspend fun <T> walk(
    media: Media,
    candidates: List<Candidate<T>>,
    clock: MonotonicClock,
    progress: (List<Ranked<T>>) -> Unit = {},
): WalkResult<T> {
    val ranked = rank(media, candidates)
    // A duration the app gave is known from the start, also for candidates the walk never reaches.
    val deltas = Array(ranked.size) { i -> countedDuration(ranked[i].durationMs)?.let { it - media.durationMs } }
    fun publish() = progress(ranked.mapIndexed { i, c -> Ranked(c, deltas[i]) })
    publish()
    val probed = mutableListOf<String>()
    var probes = 0
    var spent = 0L
    var budgetGone = false
    var closest: Int? = null
    for ((i, c) in ranked.withIndex()) {
        val known = countedDuration(c.durationMs)
        val duration: Long? = when {
            known != null -> known
            c.probe != null -> {
                if (probes == Owp.MAX_PROBES || budgetGone) continue
                val left = Owp.PROBE_BUDGET_MS - spent
                val started = clock.nowMs()
                // One extra millisecond so a probe that ends exactly at its deadline still counts.
                val result = withTimeoutOrNull(left + 1) { runProbe(c.probe) }
                spent += clock.nowMs() - started
                if (result == null || spent > Owp.PROBE_BUDGET_MS) {
                    budgetGone = true
                    continue
                }
                probes++
                probed += c.id
                countedDuration(result.durationMs)
            }
            else -> null
        }
        if (duration == null) continue
        val delta = duration - media.durationMs
        if (known == null) {
            deltas[i] = delta
            publish()
        }
        if (abs(delta) <= Owp.MATCH_TOLERANCE_MS) return WalkResult(ranked, probed, WalkOutcome.Matched(c, delta))
        if (closest == null || abs(delta) < abs(deltas[closest]!!)) closest = i
    }
    val outcome = closest?.let { WalkOutcome.Mismatch(ranked[it], deltas[it]!!) } ?: WalkOutcome.Unavailable
    return WalkResult(ranked, probed, outcome)
}

internal class Probed(val durationMs: Long?)

/**
 * A probe's duration; a probe that throws returned nothing, also when it throws a cancellation of its own, such as its
 * own timeout (SDK.md §9.2). Only the caller's cancellation, by the SDK, ends it with a throw.
 */
internal suspend fun runProbe(probe: suspend () -> Long?): Probed = try {
    Probed(probe())
} catch (e: Exception) {
    if (e is CancellationException) currentCoroutineContext().ensureActive()
    Probed(null)
}
