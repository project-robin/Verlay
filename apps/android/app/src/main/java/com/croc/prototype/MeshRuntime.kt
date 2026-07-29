package com.croc.prototype

import android.content.ContentValues
import android.content.Context
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.webkit.MimeTypeMap
import java.io.File
import java.net.Inet4Address
import java.net.InetAddress
import java.security.KeyStore
import java.security.SecureRandom
import java.util.Base64
import java.util.concurrent.TimeUnit
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import org.json.JSONArray
import org.json.JSONObject

enum class MeshAvailability { STOPPED, STARTING, AVAILABLE, ERROR }

data class MeshPairing(
    val id: String,
    val status: String,
    val code: String? = null,
    val peerName: String? = null,
    val error: String? = null,
)

data class MeshDevice(val id: String, val name: String, val platform: String)

data class MeshTransfer(
    val id: String,
    val direction: String,
    val deviceName: String,
    val fileName: String,
    val status: String,
    val message: String,
    val savedPath: String? = null,
)

data class MeshSnapshot(
    val availability: MeshAvailability = MeshAvailability.STOPPED,
    val deviceId: String? = null,
    val message: String = "Verlay is stopped.",
    val pairings: List<MeshPairing> = emptyList(),
    val devices: List<MeshDevice> = emptyList(),
    val transfers: List<MeshTransfer> = emptyList(),
)

object MeshStore {
    private val mutableState = MutableStateFlow(MeshSnapshot())
    val state = mutableState.asStateFlow()

    fun update(snapshot: MeshSnapshot) {
        mutableState.value = snapshot
    }
}

data class MeshCommandResult(
    val ok: Boolean,
    val data: JSONObject? = null,
    val errorCode: String? = null,
    val errorMessage: String? = null,
)

object MeshRuntime {
    private const val PUBLIC_RELAY_HOST = "croc.schollz.com"
    private const val PUBLIC_RELAY_PORT = 9009
    private const val PUBLIC_RELAY = "$PUBLIC_RELAY_HOST:$PUBLIC_RELAY_PORT"
    private const val KEY_ALIAS = "mesh-device-identity"
    private const val PREFS = "mesh_runtime"
    private const val SEED_CIPHERTEXT = "identity_seed_ciphertext"
    private const val SEED_IV = "identity_seed_iv"

    fun binary(context: Context): File = File(context.applicationInfo.nativeLibraryDir, "libverlay.so")

    fun environment(context: Context, includeIdentitySeed: Boolean = false): Map<String, String> {
        ensureLayout(context, refreshPublicRelay = includeIdentitySeed)
        val environment = mutableMapOf(
            "XDG_CONFIG_HOME" to File(context.filesDir, "mesh-config").absolutePath,
            "XDG_DATA_HOME" to File(context.filesDir, "mesh-data").absolutePath,
            "XDG_RUNTIME_DIR" to File(context.filesDir, "mesh-runtime").absolutePath,
            "TMPDIR" to context.cacheDir.absolutePath,
            "VERLAY_DEVICE_NAME" to listOf(Build.MANUFACTURER, Build.MODEL)
                .map(String::trim)
                .filter(String::isNotEmpty)
                .distinct()
                .joinToString(" "),
        )
        if (includeIdentitySeed) {
            environment["MESH_IDENTITY_SEED"] = identitySeed(context)
        }
        return environment
    }

    fun command(context: Context, vararg args: String, timeoutSeconds: Long = 20): MeshCommandResult {
        val binary = binary(context)
        if (!binary.isFile) {
            return MeshCommandResult(false, errorCode = "engine_missing", errorMessage = "Verlay engine is missing from this build.")
        }
        val process = try {
            ProcessBuilder(listOf(binary.absolutePath, "--json") + args)
                .redirectErrorStream(true)
                .apply { environment().putAll(environment(context)) }
                .start()
        } catch (error: Exception) {
            return MeshCommandResult(
                false,
                errorCode = "engine_start_failed",
                errorMessage = error.message ?: "Verlay command could not start.",
            )
        }
        val finished = process.waitFor(timeoutSeconds, TimeUnit.SECONDS)
        if (!finished) {
            process.destroyForcibly()
            return MeshCommandResult(false, errorCode = "timeout", errorMessage = "Verlay command timed out.")
        }
        val output = process.inputStream.bufferedReader().readText().lineSequence().lastOrNull { it.isNotBlank() }.orEmpty()
        if (output.isBlank()) {
            return MeshCommandResult(false, errorCode = "empty_response", errorMessage = "Verlay returned no response.")
        }
        return try {
            val envelope = JSONObject(output)
            if (envelope.optBoolean("ok")) {
                MeshCommandResult(true, envelope.optJSONObject("data") ?: JSONObject())
            } else {
                val error = envelope.optJSONObject("error")
                MeshCommandResult(
                    false,
                    errorCode = error?.optString("code") ?: "mesh_error",
                    errorMessage = error?.optString("message") ?: "Verlay command failed.",
                )
            }
        } catch (error: Exception) {
            MeshCommandResult(false, errorCode = "invalid_response", errorMessage = error.message ?: "Invalid Verlay response.")
        }
    }

    fun refresh(context: Context): MeshSnapshot {
        val status = command(context, "status", timeoutSeconds = 5)
        if (!status.ok) {
            return MeshSnapshot(
                availability = if (status.errorCode == "daemon_unavailable") MeshAvailability.STARTING else MeshAvailability.ERROR,
                message = status.errorMessage ?: "Verlay is unavailable.",
            )
        }
        val pairings = command(context, "pair", "list", timeoutSeconds = 5).data
            ?.optJSONArray("pairings").toPairings()
        val devices = command(context, "devices", "list", timeoutSeconds = 5).data
            ?.optJSONArray("devices").toDevices()
        val transfers = command(context, "transfers", "list", timeoutSeconds = 5).data
            ?.optJSONArray("transfers").toTransfers()
        processCompletedTransfers(context, transfers)
        return MeshSnapshot(
            availability = MeshAvailability.AVAILABLE,
            deviceId = status.data?.optString("deviceId"),
            message = "Available to paired devices",
            pairings = pairings,
            devices = devices,
            transfers = transfers,
        )
    }

    fun createPairing(context: Context): MeshCommandResult = command(context, "pair", "create")
    fun acceptPairing(context: Context, code: String): MeshCommandResult = command(context, "pair", "accept", code)
    fun revokeDevice(context: Context, id: String): MeshCommandResult =
        command(context, "devices", "revoke", id)

    fun decideTransfer(context: Context, id: String, approve: Boolean): MeshCommandResult {
        val args = if (approve) {
            arrayOf("transfers", "approve", id, "--out", File(context.filesDir, "mesh-received").absolutePath)
        } else {
            arrayOf("transfers", "reject", id)
        }
        return command(context, *args)
    }

    fun sendFile(context: Context, deviceId: String, source: File): MeshCommandResult {
        val result = command(context, "--background", "send", source.absolutePath, "--to", deviceId)
        val transferId = result.data?.optString("transferId").orEmpty()
        if (result.ok && transferId.isNotBlank()) {
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
                .edit().putString("outgoing_$transferId", source.absolutePath).apply()
        }
        return result
    }

    private fun ensureLayout(context: Context, refreshPublicRelay: Boolean) {
        val configRoot = File(context.filesDir, "mesh-config/mesh").apply { mkdirs() }
        File(context.filesDir, "mesh-data").mkdirs()
        File(context.filesDir, "mesh-runtime").mkdirs()
        File(context.filesDir, "mesh-received").mkdirs()
        val config = File(configRoot, "config.json")
        if (!config.exists() || refreshPublicRelay) {
            val relayAddress = if (refreshPublicRelay) {
                try {
                    val address = InetAddress.getAllByName(PUBLIC_RELAY_HOST)
                        .filterIsInstance<Inet4Address>()
                        .firstOrNull()
                        ?: error("Android could not resolve the Verlay relay.")
                    "${address.hostAddress}:$PUBLIC_RELAY_PORT"
                } catch (error: Exception) {
                    throw IllegalStateException(
                        "Could not resolve the public relay (${error.javaClass.simpleName}). Check the phone's network and try again.",
                        error,
                    )
                }
            } else {
                PUBLIC_RELAY
            }
            config.writeText(
                JSONObject()
                    .put("relayAddress", relayAddress)
                    .put("receiveDir", File(context.filesDir, "mesh-received").absolutePath)
                    .toString(2) + "\n",
            )
        }
    }

    @Synchronized
    private fun identitySeed(context: Context): String {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val encodedCiphertext = preferences.getString(SEED_CIPHERTEXT, null)
        val encodedIv = preferences.getString(SEED_IV, null)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        val key = identityWrappingKey()
        val seed = if (encodedCiphertext == null || encodedIv == null) {
            ByteArray(32).also(SecureRandom()::nextBytes).also { freshSeed ->
                cipher.init(Cipher.ENCRYPT_MODE, key)
                val encrypted = cipher.doFinal(freshSeed)
                preferences.edit()
                    .putString(SEED_CIPHERTEXT, Base64.getEncoder().encodeToString(encrypted))
                    .putString(SEED_IV, Base64.getEncoder().encodeToString(cipher.iv))
                    .apply()
            }
        } else {
            cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, Base64.getDecoder().decode(encodedIv)))
            cipher.doFinal(Base64.getDecoder().decode(encodedCiphertext))
        }
        return Base64.getUrlEncoder().withoutPadding().encodeToString(seed)
    }

    private fun identityWrappingKey(): SecretKey {
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (keyStore.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        generator.init(
            KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .build(),
        )
        return generator.generateKey()
    }

    private fun processCompletedTransfers(context: Context, transfers: List<MeshTransfer>) {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        transfers.filter { it.status in setOf("completed", "failed", "rejected", "cancelled") }.forEach { transfer ->
            preferences.getString("outgoing_${transfer.id}", null)?.let { path ->
                val staged = File(path)
                staged.delete()
                staged.parentFile?.delete()
                preferences.edit().remove("outgoing_${transfer.id}").apply()
            }
            if (transfer.direction == "incoming" && transfer.status == "completed" && transfer.savedPath != null &&
                !preferences.getBoolean("published_${transfer.id}", false)
            ) {
                if (publishToDownloads(context, File(transfer.savedPath), transfer.fileName)) {
                    preferences.edit().putBoolean("published_${transfer.id}", true).apply()
                }
            }
        }
    }

    private fun publishToDownloads(context: Context, source: File, displayName: String): Boolean {
        if (!source.isFile) return false
        val mimeType = MimeTypeMap.getSingleton().getMimeTypeFromExtension(source.extension.lowercase())
            ?: "application/octet-stream"
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, SafeFileNames.fromDisplayName(displayName))
            put(MediaStore.Downloads.MIME_TYPE, mimeType)
            put(MediaStore.Downloads.RELATIVE_PATH, "${Environment.DIRECTORY_DOWNLOADS}/Verlay")
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val destination = context.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: return false
        try {
            val output = context.contentResolver.openOutputStream(destination, "w")
                ?: run {
                    context.contentResolver.delete(destination, null, null)
                    return false
                }
            output.use {
                source.inputStream().use { it.copyTo(output) }
            }
            values.clear()
            values.put(MediaStore.Downloads.IS_PENDING, 0)
            context.contentResolver.update(destination, values, null, null)
            source.delete()
            source.parentFile?.delete()
            return true
        } catch (_: Exception) {
            context.contentResolver.delete(destination, null, null)
            return false
        }
    }
}

private fun JSONArray?.toPairings(): List<MeshPairing> = buildList {
    val array = this@toPairings ?: return@buildList
    for (index in 0 until array.length()) {
        val item = array.getJSONObject(index)
        add(
            MeshPairing(
                id = item.optString("pairingId"),
                status = item.optString("status"),
                code = item.stringOrNull("code"),
                peerName = item.stringOrNull("peerName"),
                error = item.stringOrNull("errorMessage"),
            ),
        )
    }
}

private fun JSONArray?.toDevices(): List<MeshDevice> = buildList {
    val array = this@toDevices ?: return@buildList
    for (index in 0 until array.length()) {
        val item = array.getJSONObject(index)
        add(MeshDevice(item.optString("deviceId"), item.optString("name"), item.optString("platform")))
    }
}

private fun JSONArray?.toTransfers(): List<MeshTransfer> = buildList {
    val array = this@toTransfers ?: return@buildList
    for (index in 0 until array.length()) {
        val item = array.getJSONObject(index)
        add(
            MeshTransfer(
                id = item.optString("transferId"),
                direction = item.optString("direction"),
                deviceName = item.optString("deviceName"),
                fileName = item.optString("fileName"),
                status = item.optString("status"),
                message = item.optString("message"),
                savedPath = item.stringOrNull("savedPath"),
            ),
        )
    }
}

private fun JSONObject.stringOrNull(key: String): String? =
    optString(key).takeIf { has(key) && it.isNotBlank() && it != "null" }

object SafeFileNames {
    fun fromDisplayName(displayName: String?): String {
        val cleaned = displayName
            ?.replace(Regex("[\\p{Cc}/\\\\]"), "_")
            ?.trim()
            ?.take(180)
            .orEmpty()
        return cleaned.takeUnless { it.isBlank() || it == "." || it == ".." } ?: "transfer.bin"
    }
}
