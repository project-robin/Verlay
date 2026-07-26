package com.croc.prototype

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

enum class TransferStatus {
    IDLE,
    PREPARING,
    WAITING,
    TRANSFERRING,
    COMPLETED,
    FAILED,
    CANCELLED,
}

enum class TransferDirection {
    SEND,
    RECEIVE,
}

data class TransferSnapshot(
    val status: TransferStatus = TransferStatus.IDLE,
    val direction: TransferDirection? = null,
    val fileName: String? = null,
    val code: String? = null,
    val message: String = "Choose what you want to do.",
    val savedLocation: String? = null,
) {
    val isActive: Boolean
        get() = status == TransferStatus.PREPARING ||
            status == TransferStatus.WAITING ||
            status == TransferStatus.TRANSFERRING
}

object TransferStore {
    private val mutableState = MutableStateFlow(TransferSnapshot())
    val state = mutableState.asStateFlow()

    fun update(snapshot: TransferSnapshot) {
        mutableState.value = snapshot
    }

    fun reset() {
        mutableState.value = TransferSnapshot()
    }
}
