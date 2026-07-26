package com.croc.prototype

import java.io.File
import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue

class CrocProcessTest {
    @Test
    fun `extracts generated code`() {
        val event = CrocOutputParser.parse("\rCode is: alpha-bravo-charlie")

        assertEquals("alpha-bravo-charlie", assertIs<CrocOutputEvent.Code>(event).value)
    }

    @Test
    fun `detects transfer connection lines`() {
        assertIs<CrocOutputEvent.TransferStarted>(CrocOutputParser.parse("Sending (->203.0.113.4)"))
        assertIs<CrocOutputEvent.TransferStarted>(CrocOutputParser.parse("Receiving (<-203.0.113.4)"))
    }

    @Test
    fun `ignores unrelated terminal output`() {
        assertNull(CrocOutputParser.parse("Sending example.txt (10 kB)"))
    }

    @Test
    fun `send command keeps global and command flags in the correct order`() {
        val command = CrocCommandFactory.send(File("/engine/croc"), File("/cache/file.txt"))

        assertEquals(listOf("--relay", "croc.schollz.com:9009"), command.slice(1..2))
        assertEquals("--ignore-stdin", command[3])
        assertEquals("send", command[5])
        assertTrue("--no-local" !in command)
        assertEquals("/cache/file.txt", command.last())
    }

    @Test
    fun `receive command never includes the secret`() {
        val command = CrocCommandFactory.receive(File("/engine/croc"), File("/incoming"))

        assertEquals(
            listOf(
                "/engine/croc",
                "--relay",
                "croc.schollz.com:9009",
                "--yes",
                "--overwrite",
                "--out",
                "/incoming",
            ),
            command,
        )
        assertTrue(command.none { it.contains("secret") })
    }

    @Test
    fun `resolved relay address can bypass Go DNS`() {
        val command = CrocCommandFactory.receive(
            File("/engine/croc"),
            File("/incoming"),
            "192.0.2.10:9009",
        )

        assertEquals("192.0.2.10:9009", command[2])
    }

    @Test
    fun `sanitizes untrusted display names`() {
        assertEquals(".._private_evil.txt", SafeFileNames.fromDisplayName("../private\\evil.txt"))
        assertEquals("transfer.bin", SafeFileNames.fromDisplayName(".."))
        assertEquals("transfer.bin", SafeFileNames.fromDisplayName(null))
    }
}
