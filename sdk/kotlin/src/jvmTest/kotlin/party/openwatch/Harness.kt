package party.openwatch

import io.ktor.client.HttpClient
import io.ktor.client.engine.okhttp.OkHttp
import io.ktor.client.plugins.websocket.WebSockets
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExecutorCoroutineDispatcher
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import java.io.BufferedInputStream
import java.io.ByteArrayOutputStream
import java.io.DataInputStream
import java.io.IOException
import java.io.OutputStream
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.security.MessageDigest
import java.util.Base64
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.concurrent.thread
import kotlin.test.AfterTest
import kotlin.test.fail

/** Ports 18300–18349 belong to this SDK's tests. */
internal object Ports {
    private val next = AtomicInteger(18300)

    fun take(): Int = next.getAndIncrement().also { check(it <= 18349) { "out of test ports" } }
}

/**
 * A relay built from relay/ by the buildRelay task, on 127.0.0.1:[port] in test mode (timers × 0.1) with the per-IP
 * limits raised, so every client of the tests can share one IP.
 */
internal class RelayProcess(val port: Int) : AutoCloseable {
    private val process: Process

    init {
        val binary = System.getProperty("owp.relay") ?: fail("system property owp.relay (the relay binary) is not set")
        val args = listOf(
            binary,
            "--addr", "127.0.0.1:$port",
            "--admin-addr", "",
            "--timer-scale", "0.1",
            "--max-connections-per-ip", "1000",
            "--max-rooms-per-ip", "1000",
            "--message-burst", "1000",
            "--messages-per-second", "1000",
            "--join-burst", "1000",
            "--joins-per-second", "1000",
        )
        process = ProcessBuilder(args).redirectError(ProcessBuilder.Redirect.INHERIT).start()
        val line = process.inputStream.bufferedReader().readLine()
        if (line == null || !line.startsWith("listening")) {
            process.destroyForcibly()
            fail("the relay didn't start: $line")
        }
        // Keep draining stdout so the relay never blocks on it.
        thread(isDaemon = true) { process.inputStream.copyTo(java.io.OutputStream.nullOutputStream()) }
    }

    /** SIGTERM: the relay closes every connection with 1001. */
    override fun close() {
        if (!process.isAlive) return
        process.destroy()
        if (!process.waitFor(10, TimeUnit.SECONDS)) process.destroyForcibly().waitFor()
    }
}

/** A TCP proxy to a relay whose connections the test can cut, as a network failure would. */
internal class TcpProxy(val port: Int, private val targetPort: Int) : AutoCloseable {
    private val server = ServerSocket().apply {
        reuseAddress = true
        bind(InetSocketAddress("127.0.0.1", port))
    }
    private val open = ConcurrentHashMap.newKeySet<Socket>()

    init {
        thread(isDaemon = true) {
            while (!server.isClosed) {
                val client = try {
                    server.accept()
                } catch (e: IOException) {
                    break
                }
                val upstream = try {
                    Socket("127.0.0.1", targetPort)
                } catch (e: IOException) {
                    client.close()
                    continue
                }
                open += client
                open += upstream
                pipe(client, upstream)
                pipe(upstream, client)
            }
        }
    }

    private fun pipe(from: Socket, to: Socket) = thread(isDaemon = true) {
        try {
            from.getInputStream().copyTo(to.getOutputStream())
        } catch (e: IOException) {
            // Cut or closed.
        } finally {
            from.close()
            to.close()
            open -= from
            open -= to
        }
    }

    /** Drops every open connection without a WebSocket close. */
    fun cut() {
        open.toList().forEach { it.close() }
    }

    override fun close() {
        server.close()
        cut()
    }
}

/**
 * A WebSocket server on 127.0.0.1:[port] that stands in for a relay: it answers every `ping` with a `pong` and every
 * other text message with what [answer] returns for it, if anything. It speaks just enough of RFC 6455 for that.
 */
internal class ScriptedRelay(val port: Int, private val answer: (JsonObject) -> String?) : AutoCloseable {
    private val server = ServerSocket().apply {
        reuseAddress = true
        bind(InetSocketAddress("127.0.0.1", port))
    }
    private val open = ConcurrentHashMap.newKeySet<Socket>()

    val url: String = "ws://127.0.0.1:$port/v1/ws"

    init {
        thread(isDaemon = true) {
            while (!server.isClosed) {
                val socket = try {
                    server.accept()
                } catch (e: IOException) {
                    break
                }
                open += socket
                thread(isDaemon = true) {
                    try {
                        serve(socket)
                    } catch (e: IOException) {
                        // Closed.
                    } finally {
                        socket.close()
                        open -= socket
                    }
                }
            }
        }
    }

    /** The upgrade, then one text frame per message; a request that isn't an upgrade is closed unanswered. */
    private fun serve(socket: Socket) {
        val input = DataInputStream(BufferedInputStream(socket.getInputStream()))
        val output = socket.getOutputStream()
        val headers = generateSequence { readLine(input) }.takeWhile { it.isNotEmpty() }.toList()
        val key = headers.firstOrNull { it.substringBefore(':').trim().equals("Sec-WebSocket-Key", ignoreCase = true) }
            ?.substringAfter(':')?.trim() ?: return
        val accept = Base64.getEncoder().encodeToString(MessageDigest.getInstance("SHA-1").digest((key + WEBSOCKET_GUID).toByteArray()))
        output.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: $accept\r\n\r\n".toByteArray())
        output.flush()
        while (true) {
            val first = input.readUnsignedByte()
            val second = input.readUnsignedByte()
            val length = when (val n = second and 0x7F) {
                126 -> input.readUnsignedShort()
                127 -> input.readLong().toInt()
                else -> n
            }
            val mask = ByteArray(4).also { if (second and 0x80 != 0) input.readFully(it) }
            val payload = ByteArray(length).also { input.readFully(it) }
            for (i in payload.indices) payload[i] = (payload[i].toInt() xor mask[i % 4].toInt()).toByte()
            when (first and 0x0F) {
                0x8 -> return
                0x1 -> {
                    val message = Json.parseToJsonElement(payload.decodeToString()).jsonObject
                    val reply = if (message["type"]?.jsonPrimitive?.content == "ping") {
                        buildJsonObject {
                            put("type", "pong")
                            put("id", message.getValue("id"))
                            put("serverTime", System.currentTimeMillis())
                        }.toString()
                    } else {
                        answer(message)
                    }
                    if (reply != null) send(output, reply)
                }
            }
        }
    }

    private fun readLine(input: DataInputStream): String? {
        val line = StringBuilder()
        while (true) {
            when (val b = input.read()) {
                -1 -> return null
                '\n'.code -> return line.toString().removeSuffix("\r")
                else -> line.append(b.toChar())
            }
        }
    }

    /** One unmasked text frame, as a server sends it. */
    private fun send(output: OutputStream, text: String) {
        val payload = text.toByteArray()
        val header = ByteArrayOutputStream()
        header.write(0x81)
        when {
            payload.size < 126 -> header.write(payload.size)
            payload.size <= 0xFFFF -> {
                header.write(126)
                header.write(payload.size shr 8)
                header.write(payload.size and 0xFF)
            }
            else -> {
                header.write(127)
                for (shift in 56 downTo 0 step 8) header.write((payload.size.toLong() shr shift).toInt() and 0xFF)
            }
        }
        output.write(header.toByteArray())
        output.write(payload)
        output.flush()
    }

    override fun close() {
        server.close()
        open.toList().forEach { it.close() }
    }

    private companion object {
        const val WEBSOCKET_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
    }
}

/** A player that plays in real time: commands take effect at once. */
internal class FakePlayer(private val clock: MonotonicClock, override val durationMs: Long?) : PlayerAdapter {
    @Volatile private var base = 0L

    @Volatile private var baseAt = clock.nowMs()

    @Volatile var rate = 1.0
        private set

    @Volatile override var paused = true
        private set

    override val positionMs: Long
        get() {
            if (paused) return base
            val p = base + ((clock.nowMs() - baseAt) * rate).toLong()
            return durationMs?.let { minOf(p, it) } ?: p
        }

    override val buffering: Boolean get() = false

    override val firstFrameShown: Boolean get() = true

    override val canSetRate: Boolean get() = true

    override suspend fun play() {
        rebase()
        paused = false
    }

    override suspend fun pause() {
        rebase()
        paused = true
    }

    override suspend fun seek(positionMs: Long) {
        base = positionMs
        baseAt = clock.nowMs()
    }

    override suspend fun setRate(rate: Double) {
        rebase()
        this.rate = rate
    }

    private fun rebase() {
        base = positionMs
        baseAt = clock.nowMs()
    }
}

/**
 * Per-test resources: one thread for every party (the SDK's threading rule), a Ktor client and the relays and proxies
 * the test started, all stopped after it.
 */
internal abstract class RelayTest {
    protected val clock: MonotonicClock = MonotonicClock.System
    private val dispatcher: ExecutorCoroutineDispatcher = Executors.newSingleThreadExecutor().asCoroutineDispatcher()
    protected val scope = CoroutineScope(SupervisorJob() + dispatcher)
    protected val http = HttpClient(OkHttp) { install(WebSockets) }
    private val resources = mutableListOf<AutoCloseable>()

    protected fun <R : AutoCloseable> use(resource: R): R = resource.also { resources += it }

    protected fun relay(port: Int = Ports.take()): RelayProcess = use(RelayProcess(port))

    protected fun relaysAt(port: Int): Relays = Relays.at(http, "http://127.0.0.1:$port")

    protected fun party(
        relays: Relays,
        name: String,
        resolver: Resolver<String> = Resolver { emptyList() },
    ): WatchParty<String> = WatchParty(relays, "OwpKotlinTest/1.0", name, resolver, scope)

    /** Runs [block] on the parties' thread, failing after [timeoutMs]. */
    protected fun test(timeoutMs: Long = 30_000, block: suspend CoroutineScope.() -> Unit) {
        runBlocking(dispatcher) { withTimeout(timeoutMs) { block() } }
    }

    /** Waits until [condition] holds, checking every 20 ms; fails after [timeoutMs] with [what]. */
    protected suspend fun eventually(what: String, timeoutMs: Long = 10_000, condition: () -> Boolean) {
        val deadline = clock.nowMs() + timeoutMs
        while (!condition()) {
            if (clock.nowMs() > deadline) fail("timed out waiting for: $what")
            delay(20)
        }
    }

    /** Every event [party] emits from now on. */
    protected fun recordEvents(party: WatchParty<*>): List<PartyEvent> {
        val events = CopyOnWriteArrayList<PartyEvent>()
        scope.launch(start = CoroutineStart.UNDISPATCHED) { party.events.collect { events += it } }
        return events
    }

    @AfterTest
    fun stopEverything() {
        scope.cancel()
        http.close()
        resources.asReversed().forEach { runCatching { it.close() } }
        dispatcher.close()
    }
}
