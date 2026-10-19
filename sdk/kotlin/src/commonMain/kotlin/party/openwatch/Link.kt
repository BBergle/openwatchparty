package party.openwatch

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * One WebSocket to a relay with its own clock (SDK.md §5): pings at open, +500 and +1000 ms, then every 5 s from open;
 * closed after 15 s with nothing received. Runs on [scope]; [onMessage] gets every message but `pong`, [onClosed] the
 * close status when the connection ends by itself (never after [close]).
 */
internal class Link(
    val url: String,
    private val socket: Socket,
    private val scope: CoroutineScope,
    private val clock: MonotonicClock,
) {
    val sync = ClockSync()
    val openedAtMs: Long = clock.nowMs()

    var onMessage: (In) -> Unit = {}
    var onPong: () -> Unit = {}
    var onClosed: (code: Int?) -> Unit = {}

    private var nextPingId = 1L
    private var ended = false
    private val jobs = mutableListOf<Job>()
    private var silence: Job? = null

    fun start() {
        jobs += scope.launch {
            for (text in socket.incoming) {
                if (ended) return@launch
                armSilence()
                when (val message = In.parse(text)) {
                    null -> {}
                    is In.Pong -> pong(message)
                    else -> onMessage(message)
                }
            }
            if (ended) return@launch
            val code = socket.closeCode()
            if (!ended) end(code)
        }
        jobs += scope.launch {
            ping()
            for (i in 1 until Owp.BURST_SIZE) {
                waitUntil(i * Owp.BURST_SPACING_MS)
                ping()
            }
            var n = 1L
            while (true) {
                waitUntil(n++ * Owp.PING_INTERVAL_MS)
                ping()
            }
        }
        armSilence()
    }

    /** False once the connection ended or was closed. */
    val isOpen: Boolean get() = !ended

    fun send(text: String) {
        if (!ended) socket.send(text)
    }

    /** Relay time now; null before the first clock sample. */
    fun relayNow(): Long? = sync.relayTime(clock.nowMs())

    /** Closes the connection; [onClosed] isn't called. */
    fun close(code: Int = 1000) {
        if (ended) return
        ended = true
        stopJobs()
        socket.close(code)
    }

    private fun end(code: Int?) {
        ended = true
        stopJobs()
        socket.close(1000)
        onClosed(code)
    }

    private fun stopJobs() {
        silence?.cancel()
        jobs.forEach { it.cancel() }
        jobs.clear()
    }

    private fun armSilence() {
        silence?.cancel()
        silence = scope.after(Owp.SILENCE_TIMEOUT_MS) { if (!ended) end(null) }
    }

    private suspend fun waitUntil(sinceOpenMs: Long) {
        delay(openedAtMs + sinceOpenMs - clock.nowMs())
    }

    private fun ping() {
        val id = nextPingId++
        sync.ping(id, clock.nowMs())
        socket.send(Out.ping(id))
    }

    private fun pong(message: In.Pong) {
        sync.pong(message.id, message.serverTime, clock.nowMs())
        onPong()
    }
}
