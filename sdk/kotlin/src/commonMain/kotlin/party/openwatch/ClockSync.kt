package party.openwatch

import kotlin.math.abs

/**
 * One connection's estimate of the relay's clock (SDK.md §5): a window of the 8 newest samples, the best one being the
 * lowest round trip (the oldest on a tie). Relay time now ≈ local now + [best]'s offset.
 */
internal class ClockSync {
    class Sample(val offsetMs: Long, val rttMs: Long)

    enum class Result { ACCEPTED, STEPPED, IGNORED }

    private val pending = mutableMapOf<Long, Long>()
    private val window = ArrayDeque<Sample>()

    /** The sample in use; null before the first. */
    var best: Sample? = null
        private set

    /** Ping [id] went out at local time [atMs]; pings unanswered for 10 s are forgotten. */
    fun ping(id: Long, atMs: Long) {
        pending.entries.removeAll { atMs - it.value >= Owp.PING_FORGET_MS }
        pending[id] = atMs
    }

    fun pong(id: Long, serverTime: Long, atMs: Long): Result {
        val sentAt = pending.remove(id) ?: return Result.IGNORED
        val rtt = atMs - sentAt
        if (rtt < 0 || rtt > Owp.MAX_RTT_MS) return Result.IGNORED
        val offset = serverTime - (sentAt + rtt / 2)
        var result = Result.ACCEPTED
        best?.let { b ->
            if (abs(offset - b.offsetMs) > (rtt + b.rttMs) / 2 + Owp.CLOCK_STEP_SLACK_MS) {
                window.clear()
                result = Result.STEPPED
            }
        }
        window.addLast(Sample(offset, rtt))
        if (window.size > Owp.CLOCK_WINDOW) window.removeFirst()
        best = window.reduce { a, b -> if (b.rttMs < a.rttMs) b else a }
        return result
    }

    /** Relay time at local time [localMs]; null before the first sample. */
    fun relayTime(localMs: Long): Long? = best?.let { localMs + it.offsetMs }
}
