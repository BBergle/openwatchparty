package party.openwatch

import kotlin.time.TimeSource

/**
 * Local time in milliseconds for everything the SDK measures: ping round trips, timers' deadlines, the time since a
 * seek. Monotonic: a wall-clock step never moves it. Tests pass a virtual clock that follows their scheduler.
 */
public fun interface MonotonicClock {
    public fun nowMs(): Long

    public companion object {
        /** [TimeSource.Monotonic] in milliseconds since the first reading in this process. */
        public val System: MonotonicClock = object : MonotonicClock {
            private val start = TimeSource.Monotonic.markNow()

            override fun nowMs(): Long = start.elapsedNow().inWholeMilliseconds
        }
    }
}
