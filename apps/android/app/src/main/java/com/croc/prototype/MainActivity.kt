package com.croc.prototype

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.database.Cursor
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.OpenableColumns
import android.widget.Toast
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
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
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
    val snapshot by TransferStore.state.collectAsStateWithLifecycle()
    val meshSnapshot by MeshStore.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    var receiveCode by rememberSaveable { mutableStateOf("") }
    var validationMessage by remember { mutableStateOf<String?>(null) }
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

    val filePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) {
            requestNotificationPermission()
            val name = queryDisplayName(context, uri)
            startTransferService(
                context,
                Intent(context, TransferService::class.java)
                    .setAction(TransferService.ACTION_SEND)
                    .putExtra(TransferService.EXTRA_URI, uri.toString())
                    .putExtra(TransferService.EXTRA_FILE_NAME, name),
            )
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
                text = "Verlay",
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
                onPairDecision = { pairing, approve ->
                    scope.launch {
                        val result = withContext(Dispatchers.IO) { MeshRuntime.decidePairing(context, pairing.id, approve) }
                        meshMessage = if (result.ok) "Pairing decision sent." else result.errorMessage
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

            HorizontalDivider(Modifier.padding(vertical = 28.dp))
            Text("Legacy Croc code transfer", style = MaterialTheme.typography.titleMedium)
            Text(
                "Kept for compatibility while the paired-device beta is tested.",
                modifier = Modifier.padding(top = 4.dp),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            if (snapshot.status != TransferStatus.IDLE) {
                Spacer(Modifier.height(24.dp))
                TransferStatusCard(
                    snapshot = snapshot,
                    onCopyCode = {
                        snapshot.code?.let { copyCode(context, it) }
                    },
                    onCancel = {
                        startTransferService(
                            context,
                            Intent(context, TransferService::class.java)
                                .setAction(TransferService.ACTION_CANCEL),
                        )
                    },
                    onReset = TransferStore::reset,
                )
            }

            Spacer(Modifier.height(28.dp))
            Text("Send from this phone", style = MaterialTheme.typography.titleMedium)
            Text(
                "Choose a file, then enter the shown code on your computer.",
                modifier = Modifier.padding(top = 4.dp, bottom = 12.dp),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Button(
                onClick = { filePicker.launch(arrayOf("*/*")) },
                enabled = !snapshot.isActive,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("Choose a file")
            }

            HorizontalDivider(Modifier.padding(vertical = 28.dp))

            Text("Receive on this phone", style = MaterialTheme.typography.titleMedium)
            Text(
                "Start croc on your computer, then enter its code below.",
                modifier = Modifier.padding(top = 4.dp, bottom = 12.dp),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            OutlinedTextField(
                value = receiveCode,
                onValueChange = {
                    receiveCode = it
                    validationMessage = null
                },
                label = { Text("Croc code") },
                supportingText = validationMessage?.let { message -> { Text(message) } },
                singleLine = true,
                enabled = !snapshot.isActive,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(12.dp))
            Button(
                onClick = {
                    val code = receiveCode.trim()
                    if (code.length < 6) {
                        validationMessage = "Enter the complete code from the sender."
                    } else {
                        requestNotificationPermission()
                        startTransferService(
                            context,
                            Intent(context, TransferService::class.java)
                                .setAction(TransferService.ACTION_RECEIVE)
                                .putExtra(TransferService.EXTRA_CODE, code),
                        )
                    }
                },
                enabled = !snapshot.isActive,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("Receive file")
            }
        }
    }
}

@Composable
private fun MeshAlphaSection(
    snapshot: MeshSnapshot,
    pairingCode: String,
    generatedCode: String?,
    message: String?,
    onPairingCodeChange: (String) -> Unit,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onCreatePairing: () -> Unit,
    onAcceptPairing: () -> Unit,
    onPairDecision: (MeshPairing, Boolean) -> Unit,
    onSendToDevice: (MeshDevice) -> Unit,
    onTransferDecision: (MeshTransfer, Boolean) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(18.dp)) {
            Text("Paired device mesh", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
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

                snapshot.pairings.filter { it.status == "pending_approval" }.forEach { pairing ->
                    Card(
                        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.secondaryContainer),
                        modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                    ) {
                        Column(Modifier.padding(14.dp)) {
                            Text(pairing.peerName ?: "New device", fontWeight = FontWeight.Bold)
                            Text(pairing.fingerprint ?: "No fingerprint", modifier = Modifier.padding(top = 4.dp))
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(top = 10.dp)) {
                                Button(onClick = { onPairDecision(pairing, true) }) { Text("Approve") }
                                OutlinedButton(onClick = { onPairDecision(pairing, false) }) { Text("Reject") }
                            }
                        }
                    }
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
                        Row(
                            modifier = Modifier.fillMaxWidth().padding(top = 10.dp),
                            horizontalArrangement = Arrangement.SpaceBetween,
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(device.name, fontWeight = FontWeight.Medium)
                                Text(device.platform, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                            OutlinedButton(onClick = { onSendToDevice(device) }) { Text("Send file") }
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

@Composable
private fun TransferStatusCard(
    snapshot: TransferSnapshot,
    onCopyCode: () -> Unit,
    onCancel: () -> Unit,
    onReset: () -> Unit,
) {
    val containerColor = when (snapshot.status) {
        TransferStatus.FAILED -> MaterialTheme.colorScheme.errorContainer
        TransferStatus.COMPLETED -> Color(0xFFE0F2E8)
        else -> MaterialTheme.colorScheme.surfaceVariant
    }
    Card(
        colors = CardDefaults.cardColors(containerColor = containerColor),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(18.dp)) {
            Text(
                text = statusTitle(snapshot.status),
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
            )
            Text(snapshot.message, modifier = Modifier.padding(top = 4.dp))
            snapshot.fileName?.let {
                Text(it, modifier = Modifier.padding(top = 8.dp), fontWeight = FontWeight.Medium)
            }
            snapshot.code?.let { code ->
                Text(
                    text = code,
                    modifier = Modifier.padding(top = 16.dp),
                    style = MaterialTheme.typography.headlineSmall,
                    fontWeight = FontWeight.Bold,
                )
                OutlinedButton(onClick = onCopyCode, modifier = Modifier.padding(top = 8.dp)) {
                    Text("Copy code")
                }
            }
            snapshot.savedLocation?.let {
                Text("Saved to $it", modifier = Modifier.padding(top = 8.dp))
            }
            Spacer(Modifier.height(14.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                if (snapshot.isActive) {
                    OutlinedButton(onClick = onCancel) { Text("Cancel") }
                } else {
                    OutlinedButton(onClick = onReset) { Text("Done") }
                }
            }
        }
    }
}

private fun statusTitle(status: TransferStatus): String = when (status) {
    TransferStatus.IDLE -> "Ready"
    TransferStatus.PREPARING -> "Preparing"
    TransferStatus.WAITING -> "Waiting"
    TransferStatus.TRANSFERRING -> "Transferring"
    TransferStatus.COMPLETED -> "Complete"
    TransferStatus.FAILED -> "Couldn’t transfer"
    TransferStatus.CANCELLED -> "Cancelled"
}

private fun startTransferService(context: Context, intent: Intent) {
    ContextCompat.startForegroundService(context, intent)
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

private fun copyCode(context: Context, code: String) {
    val clipboard = context.getSystemService(ClipboardManager::class.java)
    clipboard.setPrimaryClip(ClipData.newPlainText("Croc code", code))
    Toast.makeText(context, "Code copied", Toast.LENGTH_SHORT).show()
}

private fun queryDisplayName(context: Context, uri: Uri): String? {
    var cursor: Cursor? = null
    return try {
        cursor = context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)
        if (cursor?.moveToFirst() == true) cursor.getString(0) else null
    } finally {
        cursor?.close()
    }
}
