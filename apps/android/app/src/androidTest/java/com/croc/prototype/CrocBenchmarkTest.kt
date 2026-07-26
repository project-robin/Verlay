package com.croc.prototype

import android.content.Intent
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.fail
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class CrocBenchmarkTest {
    @Test
    fun receiveBenchmarkFile() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val code = InstrumentationRegistry.getArguments().getString("code")
            ?: error("Pass -e code <croc-code> to the instrumentation runner")
        val context = instrumentation.targetContext

        context.startForegroundService(
            Intent(context, TransferService::class.java)
                .setAction(TransferService.ACTION_RECEIVE)
                .putExtra(TransferService.EXTRA_CODE, code),
        )

        val deadlineNanos = System.nanoTime() + 180_000_000_000L
        while (System.nanoTime() < deadlineNanos) {
            val snapshot = TransferStore.state.value
            when (snapshot.status) {
                TransferStatus.COMPLETED -> return
                TransferStatus.FAILED,
                TransferStatus.CANCELLED,
                -> fail("Transfer ended with ${snapshot.status}: ${snapshot.message}")
                else -> Unit
            }
            Thread.sleep(100)
        }

        fail("Transfer did not complete within 180 seconds")
    }
}
