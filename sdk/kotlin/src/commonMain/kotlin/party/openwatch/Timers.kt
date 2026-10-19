package party.openwatch

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.yield

/**
 * [block] once [delayMs] passed, always later than the call (a zero delay still waits for the next dispatch). No
 * native delay is longer than 2147483647 ms (SDK.md §2): a longer wait runs as several, each for what is left.
 */
internal fun CoroutineScope.after(delayMs: Long, block: () -> Unit): Job = launch {
    if (delayMs > 0) {
        var left = delayMs
        while (left > 0) {
            val step = minOf(left, Owp.MAX_TIMER_MS)
            delay(step)
            left -= step
        }
    } else {
        yield()
    }
    block()
}

/** [block] every [intervalMs], the first an interval from now, until cancelled. */
internal fun CoroutineScope.every(intervalMs: Long, block: () -> Unit): Job = launch {
    while (true) {
        delay(intervalMs)
        block()
    }
}
