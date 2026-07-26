package com.croc.prototype

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

class MeshPairingUiTest {
    @get:Rule
    val compose = createAndroidComposeRule<MainActivity>()

    @Test
    fun pendingPairingRequiresVisibleMatchingCodeConfirmation() {
        var approved = false
        compose.setContent {
            MaterialTheme {
                MeshAlphaSection(
                    snapshot = MeshSnapshot(
                        availability = MeshAvailability.AVAILABLE,
                        pairings = listOf(
                            MeshPairing(
                                id = "pair-1",
                                status = "pending_approval",
                                peerName = "Editing Laptop",
                                verificationCode = "1234-5678-90AB-CDEF-1234",
                            ),
                        ),
                    ),
                    pairingCode = "",
                    generatedCode = null,
                    message = null,
                    onPairingCodeChange = {},
                    onStart = {},
                    onStop = {},
                    onCreatePairing = {},
                    onAcceptPairing = {},
                    onPairDecision = { _, decision -> approved = decision },
                    onRemoveDevice = {},
                    onSendToDevice = {},
                    onTransferDecision = { _, _ -> },
                )
            }
        }

        compose.onNodeWithText("Verify new device").assertIsDisplayed()
        compose.onNodeWithText("1234-5678-90AB-CDEF-1234").assertIsDisplayed()
        compose.onNodeWithText("Codes match").performClick()
        assertTrue(approved)
    }

    @Test
    fun removingDeviceRequiresConfirmation() {
        var removedDevice = ""
        compose.setContent {
            MaterialTheme {
                MeshAlphaSection(
                    snapshot = MeshSnapshot(
                        availability = MeshAvailability.AVAILABLE,
                        devices = listOf(MeshDevice("phone-1", "Map's Phone", "android/arm64")),
                    ),
                    pairingCode = "",
                    generatedCode = null,
                    message = null,
                    onPairingCodeChange = {},
                    onStart = {},
                    onStop = {},
                    onCreatePairing = {},
                    onAcceptPairing = {},
                    onPairDecision = { _, _ -> },
                    onRemoveDevice = { removedDevice = it.id },
                    onSendToDevice = {},
                    onTransferDecision = { _, _ -> },
                )
            }
        }

        compose.onNodeWithText("Remove").performClick()
        compose.onNodeWithText("Remove Map's Phone?").assertIsDisplayed()
        compose.onNodeWithText("Remove device").performClick()
        assertEquals("phone-1", removedDevice)
    }
}
