package com.croc.prototype

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.OpenableColumns
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import java.io.File
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (MeshDaemonService.isEnabled(this)) {
            startMeshService(this, true)
        }
        setContent {
            CrocPrototypeTheme {
                TransferScreen()
            }
        }
    }
}

@Composable
private fun CrocPrototypeTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = MaterialTheme.colorScheme.copy(
            primary = Color(0xFF075E54),
            secondary = Color(0xFF3C6255),
            background = Color(0xFFFFFDF8),
            surface = Color(0xFFFFFDF8),
        ),
        content = content,
    )
}

@Composable
private fun TransferScreen() {
    val context = androidx.compose.ui.platform.LocalContext.current
    val meshSnapshot by MeshStore.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    var meshPairingCode by rememberSaveable { mutableStateOf("") }
    var generatedMeshCode by rememberSaveable { mutableStateOf<String?>(null) }
    var meshMessage by remember { mutableStateOf<String?>(null) }
    var meshSendDevice by remember { mutableStateOf<MeshDevice?>(null) }

    val notificationPermission = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { }
    fun requestNotificationPermission() {
        if (
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) !=
            PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    val meshFilePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        val device = meshSendDevice
        if (uri != null && device != null) {
            scope.launch {
                meshMessage = "Preparing file…"
                val result = withContext(Dispatchers.IO) {
                    runCatching {
                        val staged = stageMeshFile(context, uri, queryDisplayName(context, uri))
                        MeshRuntime.sendFile(context, device.id, staged).also { sendResult ->
                            if (!sendResult.ok) {
                                staged.delete()
                                staged.parentFile?.delete()
                            }
                        }
                    }.getOrElse { error ->
                        MeshCommandResult(
                            ok = false,
                            errorCode = "file_prepare_failed",
                            errorMessage = error.message ?: "The selected file could not be prepared.",
                        )
                    }
                }
                meshMessage = if (result.ok) {
                    "Transfer request sent to ${device.name}."
                } else {
                    result.errorMessage ?: "Could not request transfer."
                }
            }
        }
        meshSendDevice = null
    }

    Surface(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp, vertical = 32.dp),
        ) {
            Text(
                text = "Verlay Mobile",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold,
            )
            Text(
                text = "Send one file securely between this phone and your computer.",
                modifier = Modifier.padding(top = 6.dp),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            Spacer(Modifier.height(24.dp))
            MeshAlphaSection(
                snapshot = meshSnapshot,
                pairingCode = meshPairingCode,
                generatedCode = generatedMeshCode,
                message = meshMessage,
                onPairingCodeChange = {
                    meshPairingCode = it
                    meshMessage = null
                },
                onStart = {
                    requestNotificationPermission()
                    startMeshService(context, true)
                },
                onStop = { startMeshService(context, false) },
                onCreatePairing = {
                    scope.launch {
                        val result = withContext(Dispatchers.IO) { MeshRuntime.createPairing(context) }
                        generatedMeshCode = result.data?.optString("code")?.takeIf { it.isNotBlank() }
                        meshMessage = result.errorMessage
                    }
                },
                onAcceptPairing = {
                    scope.launch {
                        val code = meshPairingCode.trim()
                        if (code.split('-').size != 4) {
                            meshMessage = "Enter the complete four-word pairing code."
                        } else {
                            val result = withContext(Dispatchers.IO) { MeshRuntime.acceptPairing(context, code) }
                            meshMessage = if (result.ok) "Connecting securely…" else result.errorMessage
                        }
                    }
                },
                onRemoveDevice = { device ->
                    scope.launch {
                        val result = withContext(Dispatchers.IO) { MeshRuntime.revokeDevice(context, device.id) }
                        meshMessage = if (result.ok) "${device.name} was removed." else result.errorMessage
                    }
                },
                onSendToDevice = { device ->
                    meshSendDevice = device
                    meshFilePicker.launch(arrayOf("*/*"))
                },
                onTransferDecision = { transfer, approve ->
                    scope.launch {
                        val result = withContext(Dispatchers.IO) { MeshRuntime.decideTransfer(context, transfer.id, approve) }
                        meshMessage = if (result.ok) "Transfer decision sent." else result.errorMessage
                    }
                },
            )
        }
    }
}

@Composable
internal fun MeshAlphaSection(
    snapshot: MeshSnapshot,
    pairingCode: String,
    generatedCode: String?,
    message: String?,
    onPairingCodeChange: (String) -> Unit,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onCreatePairing: () -> Unit,
    onAcceptPairing: () -> Unit,
    onRemoveDevice: (MeshDevice) -> Unit,
    onSendToDevice: (MeshDevice) -> Unit,
    onTransferDecision: (MeshTransfer, Boolean) -> Unit,
) {
    var deviceToRemove by remember { mutableStateOf<MeshDevice?>(null) }

    deviceToRemove?.let { device ->
        AlertDialog(
            onDismissRequest = { deviceToRemove = null },
            title = { Text("Remove ${device.name}?") },
            text = {
                Text(
                    "This phone will stop trusting this device. Remove this phone on the other device too if you want both lists cleared. You can pair again later.",
                )
            },
            confirmButton = {
                Button(
                    onClick = {
                        deviceToRemove = null
                        onRemoveDevice(device)
                    },
                ) {
                    Text("Remove device")
                }
            },
            dismissButton = {
                TextButton(onClick = { deviceToRemove = null }) { Text("Cancel") }
            },
        )
    }

    Card(modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(18.dp)) {
            Text("Your Verlay devices", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            Text(
                snapshot.message,
                modifier = Modifier.padding(top = 4.dp),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(12.dp))
            if (snapshot.availability == MeshAvailability.AVAILABLE || snapshot.availability == MeshAvailability.STARTING) {
                OutlinedButton(onClick = onStop, modifier = Modifier.fillMaxWidth()) { Text("Stop available mode") }
            } else {
                Button(onClick = onStart, modifier = Modifier.fillMaxWidth()) { Text("Make phone available") }
            }
            message?.let { Text(it, modifier = Modifier.padding(top = 10.dp)) }

            if (snapshot.availability == MeshAvailability.AVAILABLE) {
                HorizontalDivider(Modifier.padding(vertical = 18.dp))
                Text("Pair once", style = MaterialTheme.typography.titleMedium)
                Button(onClick = onCreatePairing, modifier = Modifier.fillMaxWidth().padding(top = 10.dp)) {
                    Text("Create pairing code")
                }
                generatedCode?.let {
                    Text(it, modifier = Modifier.padding(top = 10.dp), fontWeight = FontWeight.Bold)
                }
                OutlinedTextField(
                    value = pairingCode,
                    onValueChange = onPairingCodeChange,
                    label = { Text("Four-word code") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                )
                OutlinedButton(onClick = onAcceptPairing, modifier = Modifier.fillMaxWidth().padding(top = 10.dp)) {
                    Text("Accept code")
                }

                if (snapshot.devices.isEmpty()) {
                    snapshot.pairings.lastOrNull { it.status == "failed" }?.let { pairing ->
                        Card(
                            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer),
                            modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                        ) {
                            Column(Modifier.padding(14.dp)) {
                                Text("Pairing failed", fontWeight = FontWeight.Bold)
                                Text(
                                    if (pairing.error?.contains("lookup croc.schollz.com") == true) {
                                        "Android could not reach the relay. Check the connection and try again."
                                    } else {
                                        pairing.error ?: "The secure pairing connection failed. Try again."
                                    },
                                    modifier = Modifier.padding(top = 4.dp),
                                )
                            }
                        }
                    }
                }

                if (snapshot.devices.isNotEmpty()) {
                    HorizontalDivider(Modifier.padding(vertical = 18.dp))
                    Text("Paired devices", style = MaterialTheme.typography.titleMedium)
                    snapshot.devices.forEach { device ->
                        Column(
                            modifier = Modifier.fillMaxWidth().padding(top = 10.dp),
                        ) {
                            Text(device.name, fontWeight = FontWeight.Medium)
                            Text(device.platform, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            Row(
                                horizontalArrangement = Arrangement.spacedBy(8.dp),
                                modifier = Modifier.padding(top = 6.dp),
                            ) {
                                OutlinedButton(onClick = { onSendToDevice(device) }) { Text("Send file") }
                                TextButton(onClick = { deviceToRemove = device }) { Text("Remove") }
                            }
                        }
                    }
                }

                snapshot.transfers.filter { it.status == "pending_approval" }.forEach { transfer ->
                    Card(
                        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.tertiaryContainer),
                        modifier = Modifier.fillMaxWidth().padding(top = 14.dp),
                    ) {
                        Column(Modifier.padding(14.dp)) {
                            Text("Incoming from ${transfer.deviceName}", fontWeight = FontWeight.Bold)
                            Text(transfer.fileName, modifier = Modifier.padding(top = 4.dp))
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(top = 10.dp)) {
                                Button(onClick = { onTransferDecision(transfer, true) }) { Text("Accept") }
                                OutlinedButton(onClick = { onTransferDecision(transfer, false) }) { Text("Reject") }
                            }
                        }
                    }
                }

                snapshot.transfers.takeLast(3).filter { it.status != "pending_approval" }.forEach { transfer ->
                    Text(
                        "${transfer.fileName}: ${transfer.status}",
                        modifier = Modifier.padding(top = 8.dp),
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

private fun startMeshService(context: Context, start: Boolean) {
    val intent = Intent(context, MeshDaemonService::class.java).setAction(
        if (start) MeshDaemonService.ACTION_START else MeshDaemonService.ACTION_STOP,
    )
    if (start) ContextCompat.startForegroundService(context, intent) else context.startService(intent)
}

private fun stageMeshFile(context: Context, uri: Uri, displayName: String?): File {
    val directory = File(context.filesDir, "mesh-outgoing/${UUID.randomUUID()}").apply { mkdirs() }
    val destination = File(directory, SafeFileNames.fromDisplayName(displayName))
    context.contentResolver.openInputStream(uri).use { input ->
        requireNotNull(input) { "The selected file could not be opened." }
        destination.outputStream().use(input::copyTo)
    }
    return destination
}

private fun queryDisplayName(context: Context, uri: Uri): String? {
    return context.contentResolver
        .query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)
        ?.use { cursor ->
            if (cursor.moveToFirst()) cursor.getString(0) else null
        }
}
