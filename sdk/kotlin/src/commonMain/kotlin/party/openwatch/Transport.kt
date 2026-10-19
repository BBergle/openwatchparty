package party.openwatch

import io.ktor.client.HttpClient
import io.ktor.client.plugins.websocket.webSocketSession
import io.ktor.websocket.CloseReason
import io.ktor.websocket.Frame
import io.ktor.websocket.close
import io.ktor.websocket.readText
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.channels.ReceiveChannel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/** One WebSocket of text messages. */
internal interface Socket {
    /** Text messages as they arrive; closed when the connection ends. */
    val incoming: ReceiveChannel<String>

    /** The close status once [incoming] has ended; null for a connection that ended without one. */
    suspend fun closeCode(): Int?

    fun send(text: String)

    /** Closes with [code]; nothing more arrives. */
    fun close(code: Int)
}

/** Opens WebSockets; throws when the connection fails (HTTP 429 and 503 included). */
internal fun interface Transport {
    suspend fun open(url: String): Socket
}

/** Sockets over the app's Ktor client; their reading runs on [scope]. */
internal class KtorTransport(private val http: HttpClient, private val scope: CoroutineScope) : Transport {
    override suspend fun open(url: String): Socket {
        val session = http.webSocketSession(url)
        val incoming = Channel<String>(Channel.UNLIMITED)
        scope.launch {
            try {
                for (frame in session.incoming) if (frame is Frame.Text) incoming.send(frame.readText())
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // The connection failed; closing the channel reports it.
            } finally {
                incoming.close()
            }
        }
        return object : Socket {
            override val incoming: ReceiveChannel<String> = incoming

            override suspend fun closeCode(): Int? = withTimeoutOrNull(CLOSE_REASON_WAIT_MS) {
                try {
                    session.closeReason.await()?.code?.toInt()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    null
                }
            }

            override fun send(text: String) {
                session.outgoing.trySend(Frame.Text(text))
            }

            override fun close(code: Int) {
                scope.launch {
                    try {
                        session.close(CloseReason(code.toShort(), ""))
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        // Already closed.
                    }
                }
            }
        }
    }

    private companion object {
        const val CLOSE_REASON_WAIT_MS = 1000L
    }
}
