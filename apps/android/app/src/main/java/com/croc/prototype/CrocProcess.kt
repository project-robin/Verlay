package com.croc.prototype

import java.io.File
import java.net.Inet4Address
import java.net.InetAddress

sealed interface CrocOutputEvent {
    data class Code(val value: String) : CrocOutputEvent
    data object TransferStarted : CrocOutputEvent
}

object CrocOutputParser {
    fun parse(rawLine: String): CrocOutputEvent? {
        val line = rawLine.replace('\r', ' ').trim()
        if (line.startsWith("Code is:")) {
            val code = line.substringAfter("Code is:").trim()
            if (code.isNotEmpty()) {
                return CrocOutputEvent.Code(code)
            }
        }
        if (line.startsWith("Sending (->") || line.startsWith("Receiving (<-")) {
            return CrocOutputEvent.TransferStarted
        }
        return null
    }
}

object CrocCommandFactory {
    fun send(
        binary: File,
        stagedFile: File,
        relayAddress: String = PUBLIC_RELAY,
    ): List<String> = listOf(
        binary.absolutePath,
        "--relay",
        relayAddress,
        "--ignore-stdin",
        "--disable-clipboard",
        "send",
        stagedFile.absolutePath,
    )

    fun receive(
        binary: File,
        outputDirectory: File,
        relayAddress: String = PUBLIC_RELAY,
    ): List<String> = listOf(
        binary.absolutePath,
        "--relay",
        relayAddress,
        "--yes",
        "--overwrite",
        "--out",
        outputDirectory.absolutePath,
    )

    const val PUBLIC_RELAY_HOST = "croc.schollz.com"
    const val PUBLIC_RELAY_PORT = 9009
    const val PUBLIC_RELAY = "croc.schollz.com:9009"
}

fun resolvePublicRelayAddress(): String {
    val addresses = InetAddress.getAllByName(CrocCommandFactory.PUBLIC_RELAY_HOST)
    val address = addresses.filterIsInstance<Inet4Address>().firstOrNull()
        ?: throw IllegalStateException("Android could not resolve the croc relay.")
    return "${address.hostAddress}:${CrocCommandFactory.PUBLIC_RELAY_PORT}"
}

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
