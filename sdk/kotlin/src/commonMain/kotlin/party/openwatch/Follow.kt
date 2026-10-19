package party.openwatch

import kotlin.math.abs
import kotlin.math.ceil
import kotlin.math.floor
import kotlin.math.sign
import kotlin.math.truncate

/**
 * The room position at relay time [t] (SPEC.md §12.2): in `playing`, `min(durationMs, floor(positionMs + max(0, t −
 * atMs) × rate))` in doubles, one rounding per operation; else `positionMs`.
 */
internal fun roomPosition(phase: Phase, positionMs: Long, atMs: Long, rate: Double, durationMs: Long, t: Long): Long {
    if (phase != Phase.PLAYING) return positionMs
    val elapsed = maxOf(0L, t - atMs).toDouble()
    val moved = elapsed * rate
    val sum = positionMs.toDouble() + moved
    return minOf(floor(sum).toLong(), durationMs)
}

/** Whether the room's position moves at relay time [t]. */
internal fun roomRunning(phase: Phase, atMs: Long, t: Long): Boolean = phase == Phase.PLAYING && t >= atMs

internal fun Room.positionAt(durationMs: Long, t: Long): Long = roomPosition(phase, positionMs, atMs, rate, durationMs, t)

/**
 * Relay time when local playback of a `playing` room starts (SDK.md §11.2): later than [atMs] when the local file
 * starts after the room's position, [anchor] (the local position at [atMs]) < 0.
 */
internal fun localStartAt(atMs: Long, anchor: Long, rate: Double): Double {
    if (anchor >= 0) return atMs.toDouble()
    val lead = (-anchor).toDouble() / rate
    return atMs.toDouble() + lead
}

/** What one reconciliation step reads (SDK.md §11.1). */
internal class StepInput(
    val phase: Phase,
    val positionMs: Long,
    val atMs: Long,
    val rate: Double,
    val durationMs: Long,
    val nowMs: Long,
    val offsetMs: Long,
    val playerPositionMs: Long,
    val paused: Boolean,
    val buffering: Boolean,
    val playerDurationMs: Long?,
    val canSetRate: Boolean,
    /** The adapter's seek accuracy, already at least 0. */
    val seekAccuracyMs: Long,
    /** The rate the SDK last set on the adapter. */
    val appliedRate: Double,
    val nudging: Boolean,
    /** Null when the SDK never seeked on this adapter. */
    val sinceSeekMs: Long?,
)

internal sealed interface StepAction {
    data object Pause : StepAction

    data class SetRate(val rate: Double) : StepAction

    data class Seek(val positionMs: Long) : StepAction

    data object Play : StepAction
}

internal class StepOutput(
    /** In the order pause, setRate, seek, play. */
    val actions: List<StepAction>,
    val nudging: Boolean,
    /** The play timer: run a step again in this many ms. */
    val playInMs: Long?,
)

/** One reconciliation step (SDK.md §11.2). */
internal fun reconcile(input: StepInput): StepOutput {
    val actions = mutableListOf<StepAction>()
    if (input.phase == Phase.IDLE) return StepOutput(actions, nudging = false, playInMs = null)
    val cooled = input.sinceSeekMs == null || input.sinceSeekMs >= Owp.SEEK_COOLDOWN_MS
    val end = input.playerDurationMs?.takeIf { it > 0 }?.let { maxOf(0L, it - Owp.END_MARGIN_MS) }
    fun clamp(x: Long): Long = maxOf(0L, x).let { if (end != null) minOf(it, end) else it }
    fun setRate(rate: Double) {
        if (input.canSetRate && abs(input.appliedRate - rate) > Owp.RATE_EPSILON) actions += StepAction.SetRate(round6(rate))
    }

    val anchor = input.positionMs + input.offsetMs
    var held = input.phase != Phase.PLAYING
    var playIn: Long? = null
    if (input.phase == Phase.PLAYING) {
        val startAt = localStartAt(input.atMs, anchor, input.rate)
        if (input.nowMs.toDouble() < startAt) {
            held = true
            playIn = ceil(startAt - input.nowMs.toDouble()).toLong()
        }
    }

    if (held) {
        val holdAt = clamp(anchor)
        if (!input.paused) actions += StepAction.Pause
        setRate(input.rate)
        if (abs(input.playerPositionMs - holdAt) > maxOf(Owp.HELD_SEEK_MS, input.seekAccuracyMs) && cooled) actions += StepAction.Seek(holdAt)
        return StepOutput(actions, nudging = false, playInMs = playIn)
    }

    val room = roomPosition(input.phase, input.positionMs, input.atMs, input.rate, input.durationMs, input.nowMs)
    val target = maxOf(0L, room + input.offsetMs)
    var nudging = input.nudging && input.canSetRate
    if (end != null && target >= end && input.playerPositionMs >= end) {
        setRate(input.rate)
        return StepOutput(actions, nudging = false, playInMs = null)
    }
    if (input.buffering) return StepOutput(actions, nudging = nudging, playInMs = null)

    val drift = input.playerPositionMs - target
    val size = abs(drift)
    var seek = false
    if (input.canSetRate) {
        when {
            size > maxOf(Owp.SEEK_MS, input.seekAccuracyMs) -> {
                nudging = false
                seek = cooled
            }
            nudging -> nudging = size >= Owp.NUDGE_EXIT_MS
            size > Owp.DEADBAND_MS -> nudging = true
        }
    } else if (size > maxOf(Owp.SEEK_ONLY_MS, input.seekAccuracyMs)) {
        seek = cooled
    }
    val want = if (nudging) {
        val factor = if (drift > 0) 1 - Owp.NUDGE_FACTOR else 1 + Owp.NUDGE_FACTOR
        input.rate * factor
    } else {
        input.rate
    }
    setRate(want)
    if (seek) actions += StepAction.Seek(clamp(target + Owp.SEEK_LEAD_MS))
    if (input.paused) actions += StepAction.Play
    return StepOutput(actions, nudging = nudging, playInMs = null)
}

/** Rates as sent to the adapter: rounded to 6 decimals, halves away from zero, so 1.25 × 0.96 is 1.2. */
private fun round6(x: Double): Double {
    val y = x * 1e6
    val t = truncate(y)
    val r = if (abs(y - t) >= 0.5) t + sign(y) else t
    return r / 1e6
}
