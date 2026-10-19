package party.openwatch

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlin.math.abs
import kotlin.math.floor

/** Largest magnitude of an int (SPEC.md §2). */
private const val MAX_INT: Double = 9007199254740991.0

/**
 * A JSON number with an integral value in range, as SPEC.md §2 reads an int: `3.0` is 3. Null for anything else,
 * strings included.
 */
internal fun JsonElement?.asInt(): Long? {
    val p = this as? JsonPrimitive ?: return null
    if (p.isString || p is JsonNull) return null
    p.content.toLongOrNull()?.let { return if (abs(it.toDouble()) <= MAX_INT) it else null }
    val d = p.doubleOrNull ?: return null
    return if (d == floor(d) && abs(d) <= MAX_INT) d.toLong() else null
}

internal fun JsonElement?.asNumber(): Double? {
    val p = this as? JsonPrimitive ?: return null
    if (p.isString || p is JsonNull) return null
    return p.doubleOrNull
}

internal fun JsonElement?.asString(): String? {
    val p = this as? JsonPrimitive ?: return null
    return if (p.isString) p.content else null
}

internal fun JsonElement?.asBoolean(): Boolean? {
    val p = this as? JsonPrimitive ?: return null
    if (p.isString || p is JsonNull) return null
    return p.booleanOrNull
}

internal fun JsonElement?.asObject(): JsonObject? = this as? JsonObject

internal fun JsonElement?.asArray(): JsonArray? = this as? JsonArray

/** `namespace → ID` maps: string members only. */
internal fun JsonElement?.asStringMap(): Map<String, String>? {
    val obj = asObject() ?: return null
    return buildMap { for ((k, v) in obj) v.asString()?.let { put(k, it) } }
}

/** UTF-8 byte count of [text]. */
internal fun utf8Length(text: String): Int = text.encodeToByteArray().size
