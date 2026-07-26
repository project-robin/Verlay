package com.croc.prototype

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test

class MeshPairingUiTest {
    @get:Rule
    val compose = createAndroidComposeRule<MainActivity>()

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
