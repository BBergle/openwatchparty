package party.openwatch

import kotlinx.coroutines.CancellableContinuation
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Delay
import kotlinx.coroutines.DisposableHandle
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.InternalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.Runnable
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlin.coroutines.CoroutineContext
import kotlin.test.Test
import kotlin.test.assertEquals

/** SDK.md §2: no native delay is longer than 2147483647 ms; a longer timer waits in several. */
@OptIn(ExperimentalCoroutinesApi::class, InternalCoroutinesApi::class)
class TimersTest {
    /** A test dispatcher that records every delay asked of it. */
    private class Recording(private val inner: TestDispatcher) : CoroutineDispatcher(), Delay {
        val delays = mutableListOf<Long>()

        override fun isDispatchNeeded(context: CoroutineContext): Boolean = inner.isDispatchNeeded(context)

        override fun dispatch(context: CoroutineContext, block: Runnable) = inner.dispatch(context, block)

        override fun scheduleResumeAfterDelay(timeMillis: Long, continuation: CancellableContinuation<Unit>) {
            delays += timeMillis
            inner.scheduleResumeAfterDelay(timeMillis, continuation)
        }

        override fun invokeOnTimeout(timeMillis: Long, block: Runnable, context: CoroutineContext): DisposableHandle {
            delays += timeMillis
            return inner.invokeOnTimeout(timeMillis, block, context)
        }
    }

    @Test
    fun aTimerLongerThanTheLargestDelayNeitherFiresEarlyNorLoops() = runTest {
        val recording = Recording(StandardTestDispatcher(testScheduler))
        val scope = CoroutineScope(recording + Job())
        val wait = 5_000_000_000L
        var firedAt: Long? = null
        scope.after(wait) { firedAt = testScheduler.currentTime }
        advanceTimeBy(wait - 1)
        runCurrent()
        assertEquals(null, firedAt)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(wait, firedAt)
        assertEquals(listOf(Owp.MAX_TIMER_MS, Owp.MAX_TIMER_MS, wait - 2 * Owp.MAX_TIMER_MS), recording.delays)
        scope.cancel()
    }
}
