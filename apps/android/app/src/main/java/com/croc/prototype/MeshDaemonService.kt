package com.croc.prototype

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

class MeshDaemonService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    @Volatile
    private var daemon: Process? = null
    private var monitor: Job? = null
    private var starter: Job? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            setEnabled(false)
            stopDaemon()
            MeshStore.update(MeshSnapshot())
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
            stopSelf()
            return START_NOT_STICKY
        }
        setEnabled(true)
        ServiceCompat.startForeground(
            this,
            NOTIFICATION_ID,
            notification("Starting Verlay…"),
            android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
        )
        startDaemon()
        return START_STICKY
    }

    override fun onDestroy() {
        stopDaemon()
        scope.cancel()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun startDaemon() {
        if (daemon?.isAlive == true || starter?.isActive == true) return
        MeshStore.update(MeshSnapshot(availability = MeshAvailability.STARTING, message = "Starting Verlay…"))
        starter = scope.launch {
            val binary = MeshRuntime.binary(this@MeshDaemonService)
            if (!binary.isFile) {
                MeshStore.update(MeshSnapshot(availability = MeshAvailability.ERROR, message = "Mesh engine is missing."))
                return@launch
            }
            val started = try {
                ProcessBuilder(binary.absolutePath, "daemon", "run")
                    .redirectErrorStream(true)
                    .apply {
                        environment().putAll(
                            MeshRuntime.environment(this@MeshDaemonService, includeIdentitySeed = true),
                        )
                    }
                    .start()
            } catch (error: Exception) {
                Log.e(TAG, "Verlay startup failed", error)
                val detail = error.message?.takeIf { it.isNotBlank() }
                    ?: "${error.javaClass.simpleName} while preparing the mesh engine."
                MeshStore.update(
                    MeshSnapshot(
                        availability = MeshAvailability.ERROR,
                        message = detail,
                    ),
                )
                getSystemService(NotificationManager::class.java).notify(
                    NOTIFICATION_ID,
                    notification("Mesh could not start"),
                )
                return@launch
            }
            if (!meshEnabled()) {
                started.destroy()
                return@launch
            }
            daemon = started
            scope.launch { started.inputStream.bufferedReader().useLines { lines -> lines.forEach { _ -> } } }
            startMonitor()
        }
    }

    private fun startMonitor() {
        monitor?.cancel()
        monitor = scope.launch {
            while (isActive && meshEnabled()) {
                val snapshot = MeshRuntime.refresh(this@MeshDaemonService)
                MeshStore.update(snapshot)
                val pending = snapshot.transfers.count { it.status == "pending_approval" } +
                    snapshot.pairings.count { it.status == "pending_approval" }
                getSystemService(NotificationManager::class.java).notify(
                    NOTIFICATION_ID,
                    notification(if (pending > 0) "$pending request(s) need approval" else snapshot.message),
                )
                if (daemon?.isAlive != true) {
                    delay(2_000)
                    if (meshEnabled()) startDaemon()
                    return@launch
                }
                delay(2_000)
            }
        }
    }

    private fun stopDaemon() {
        starter?.cancel()
        starter = null
        monitor?.cancel()
        monitor = null
        daemon?.destroy()
        daemon?.waitFor(2, TimeUnit.SECONDS)
        if (daemon?.isAlive == true) daemon?.destroyForcibly()
        daemon = null
    }

    private fun notification(message: String): Notification {
        val openApp = PendingIntent.getActivity(
            this,
            20,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val stop = PendingIntent.getService(
            this,
            21,
            Intent(this, MeshDaemonService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return NotificationCompat.Builder(this, NOTIFICATION_CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_upload_done)
            .setContentTitle("Mesh available")
            .setContentText(message)
            .setContentIntent(openApp)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .addAction(0, "Stop", stop)
            .build()
    }

    private fun createNotificationChannel() {
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(NOTIFICATION_CHANNEL, "Verlay availability", NotificationManager.IMPORTANCE_LOW),
        )
    }

    private fun setEnabled(enabled: Boolean) {
        getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).edit().putBoolean(ENABLED, enabled).apply()
    }

    private fun meshEnabled(): Boolean = isEnabled(this)

    companion object {
        const val ACTION_START = "com.croc.prototype.MESH_START"
        const val ACTION_STOP = "com.croc.prototype.MESH_STOP"
        private const val PREFERENCES = "mesh_service"
        private const val ENABLED = "enabled"
        private const val NOTIFICATION_CHANNEL = "mesh_availability"
        private const val NOTIFICATION_ID = 2001
        private const val TAG = "MeshDaemonService"

        fun isEnabled(context: Context): Boolean =
            context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).getBoolean(ENABLED, false)
    }
}
