package com.croc.prototype

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.ContentValues
import android.content.Intent
import android.net.Uri
import android.os.Environment
import android.os.IBinder
import android.provider.MediaStore
import android.webkit.MimeTypeMap
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import java.io.File
import java.util.UUID

class TransferService : Service() {
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var transferJob: Job? = null

    @Volatile
    private var currentProcess: Process? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_CANCEL -> cancelTransfer()
            ACTION_SEND -> startSend(intent)
            ACTION_RECEIVE -> startReceive(intent)
        }
        return START_NOT_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        currentProcess?.destroyForcibly()
        serviceScope.cancel()
        super.onDestroy()
    }

    private fun startSend(intent: Intent) {
        val source = intent.getStringExtra(EXTRA_URI)?.let(Uri::parse)
        val displayName = SafeFileNames.fromDisplayName(intent.getStringExtra(EXTRA_FILE_NAME))
        if (source == null) {
            failImmediately(TransferDirection.SEND, "The selected file is unavailable.")
            return
        }
        launchTransfer(TransferDirection.SEND, displayName) {
            runSend(source, displayName)
        }
    }

    private fun startReceive(intent: Intent) {
        val code = intent.getStringExtra(EXTRA_CODE)?.trim().orEmpty()
        if (code.length < MINIMUM_CODE_LENGTH) {
            failImmediately(TransferDirection.RECEIVE, "Enter a valid croc code.")
            return
        }
        launchTransfer(TransferDirection.RECEIVE, null) {
            runReceive(code)
        }
    }

    private fun launchTransfer(
        direction: TransferDirection,
        fileName: String?,
        block: suspend () -> Unit,
    ) {
        if (transferJob?.isActive == true) {
            return
        }
        val preparing = TransferSnapshot(
            status = TransferStatus.PREPARING,
            direction = direction,
            fileName = fileName,
            message = if (direction == TransferDirection.SEND) "Preparing file…" else "Preparing receiver…",
        )
        updateState(preparing)
        ServiceCompat.startForeground(
            this,
            NOTIFICATION_ID,
            buildNotification(preparing),
            android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
        )
        transferJob = serviceScope.launch {
            try {
                block()
            } catch (_: CancellationException) {
                updateState(
                    TransferSnapshot(
                        status = TransferStatus.CANCELLED,
                        direction = direction,
                        fileName = fileName,
                        message = "Transfer cancelled.",
                    ),
                )
            } catch (error: Exception) {
                updateState(
                    TransferSnapshot(
                        status = TransferStatus.FAILED,
                        direction = direction,
                        fileName = fileName,
                        message = error.message?.take(240) ?: "Transfer failed.",
                    ),
                )
            } finally {
                currentProcess = null
                ServiceCompat.stopForeground(this@TransferService, ServiceCompat.STOP_FOREGROUND_REMOVE)
                stopSelf()
            }
        }
    }

    private suspend fun runSend(source: Uri, displayName: String) {
        val stagingDirectory = File(cacheDir, "outgoing/${UUID.randomUUID()}")
        val stagedFile = File(stagingDirectory, displayName)
        try {
            stagingDirectory.mkdirs()
            contentResolver.openInputStream(source).use { input ->
                requireNotNull(input) { "The selected file could not be opened." }
                stagedFile.outputStream().use(input::copyTo)
            }

            val binary = crocBinary()
            runCroc(
                command = CrocCommandFactory.send(binary, stagedFile, resolvePublicRelayAddress()),
                direction = TransferDirection.SEND,
                initialFileName = displayName,
            )
            updateState(
                TransferSnapshot(
                    status = TransferStatus.COMPLETED,
                    direction = TransferDirection.SEND,
                    fileName = displayName,
                    message = "File sent successfully.",
                ),
            )
        } finally {
            stagingDirectory.deleteRecursively()
        }
    }

    private suspend fun runReceive(code: String) {
        val stagingDirectory = File(filesDir, "incoming/${UUID.randomUUID()}")
        try {
            stagingDirectory.mkdirs()
            updateState(
                TransferSnapshot(
                    status = TransferStatus.WAITING,
                    direction = TransferDirection.RECEIVE,
                    message = "Waiting for the sender…",
                ),
            )
            runCroc(
                command = CrocCommandFactory.receive(
                    crocBinary(),
                    stagingDirectory,
                    resolvePublicRelayAddress(),
                ),
                direction = TransferDirection.RECEIVE,
                environment = mapOf("CROC_SECRET" to code),
            )

            val files = stagingDirectory.walkTopDown().filter(File::isFile).toList()
            require(files.size == 1) {
                if (files.isEmpty()) "No file was received." else "This prototype accepts one file at a time."
            }
            val receivedFile = files.single()
            val savedUri = publishToDownloads(receivedFile)
            updateState(
                TransferSnapshot(
                    status = TransferStatus.COMPLETED,
                    direction = TransferDirection.RECEIVE,
                    fileName = receivedFile.name,
                    message = "File received successfully.",
                    savedLocation = savedUri.toString(),
                ),
            )
        } finally {
            stagingDirectory.deleteRecursively()
        }
    }

    private fun runCroc(
        command: List<String>,
        direction: TransferDirection,
        initialFileName: String? = null,
        environment: Map<String, String> = emptyMap(),
    ) {
        val process = ProcessBuilder(command)
            .redirectErrorStream(true)
            .apply { environment().putAll(environment) }
            .start()
        currentProcess = process

        var code: String? = null
        val recentOutput = ArrayDeque<String>()
        process.inputStream.bufferedReader().useLines { lines ->
            lines.forEach { line ->
                val trimmed = line.replace('\r', ' ').trim()
                if (trimmed.isNotBlank() && !trimmed.startsWith("Code is:")) {
                    if (recentOutput.size == MAX_ERROR_LINES) recentOutput.removeFirst()
                    recentOutput.addLast(trimmed)
                }
                when (val event = CrocOutputParser.parse(line)) {
                    is CrocOutputEvent.Code -> {
                        code = event.value
                        updateState(
                            TransferSnapshot(
                                status = TransferStatus.WAITING,
                                direction = direction,
                                fileName = initialFileName,
                                code = event.value,
                                message = "Share this code with the receiver.",
                            ),
                        )
                    }
                    CrocOutputEvent.TransferStarted -> updateState(
                        TransferSnapshot(
                            status = TransferStatus.TRANSFERRING,
                            direction = direction,
                            fileName = initialFileName,
                            code = code,
                            message = if (direction == TransferDirection.SEND) "Sending file…" else "Receiving file…",
                        ),
                    )
                    null -> Unit
                }
            }
        }

        val exitCode = process.waitFor()
        if (exitCode != 0) {
            val detail = recentOutput.lastOrNull()?.take(200)
            throw IllegalStateException(detail ?: "Croc exited with code $exitCode.")
        }
    }

    private fun crocBinary(): File {
        val binary = File(applicationInfo.nativeLibraryDir, "libcroc.so")
        require(binary.isFile) { "The embedded croc engine is missing." }
        return binary
    }

    private fun publishToDownloads(source: File): Uri {
        val extension = source.extension.lowercase()
        val mimeType = MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension)
            ?: "application/octet-stream"
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, SafeFileNames.fromDisplayName(source.name))
            put(MediaStore.Downloads.MIME_TYPE, mimeType)
            put(MediaStore.Downloads.RELATIVE_PATH, "${Environment.DIRECTORY_DOWNLOADS}/Croc")
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val destination = requireNotNull(
            contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values),
        ) { "Android could not create the destination file." }
        try {
            contentResolver.openOutputStream(destination, "w").use { output ->
                requireNotNull(output) { "Android could not open the destination file." }
                source.inputStream().use { it.copyTo(output) }
            }
            values.clear()
            values.put(MediaStore.Downloads.IS_PENDING, 0)
            contentResolver.update(destination, values, null, null)
            return destination
        } catch (error: Exception) {
            contentResolver.delete(destination, null, null)
            throw error
        }
    }

    private fun cancelTransfer() {
        currentProcess?.destroy()
        currentProcess?.destroyForcibly()
        transferJob?.cancel()
    }

    private fun failImmediately(direction: TransferDirection, message: String) {
        updateState(
            TransferSnapshot(
                status = TransferStatus.FAILED,
                direction = direction,
                message = message,
            ),
        )
        stopSelf()
    }

    private fun updateState(snapshot: TransferSnapshot) {
        TransferStore.update(snapshot)
        if (snapshot.isActive) {
            getSystemService(NotificationManager::class.java)
                .notify(NOTIFICATION_ID, buildNotification(snapshot))
        }
    }

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            NOTIFICATION_CHANNEL,
            "File transfers",
            NotificationManager.IMPORTANCE_LOW,
        )
        getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
    }

    private fun buildNotification(snapshot: TransferSnapshot): Notification {
        val openApp = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val cancel = PendingIntent.getService(
            this,
            1,
            Intent(this, TransferService::class.java).setAction(ACTION_CANCEL),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return NotificationCompat.Builder(this, NOTIFICATION_CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentTitle("Croc Transfer")
            .setContentText(snapshot.message)
            .setContentIntent(openApp)
            .setOngoing(snapshot.isActive)
            .setOnlyAlertOnce(true)
            .addAction(0, "Cancel", cancel)
            .build()
    }

    companion object {
        const val ACTION_SEND = "com.croc.prototype.SEND"
        const val ACTION_RECEIVE = "com.croc.prototype.RECEIVE"
        const val ACTION_CANCEL = "com.croc.prototype.CANCEL"
        const val EXTRA_URI = "uri"
        const val EXTRA_FILE_NAME = "file_name"
        const val EXTRA_CODE = "code"

        private const val MINIMUM_CODE_LENGTH = 6
        private const val MAX_ERROR_LINES = 12
        private const val NOTIFICATION_CHANNEL = "croc_transfers"
        private const val NOTIFICATION_ID = 1001
    }
}
