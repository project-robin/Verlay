use serde::Serialize;
use std::{
    collections::VecDeque,
    path::Path,
    sync::{
        atomic::{AtomicU64, Ordering},
        Arc, Mutex,
    },
};
use tauri::{AppHandle, Emitter, State};
use tauri_plugin_dialog::{DialogExt, FilePath};
use tauri_plugin_shell::{process::CommandChild, process::CommandEvent, ShellExt};

const PUBLIC_RELAY: &str = "croc.schollz.com:9009";
const MAX_DIAGNOSTIC_LINES: usize = 24;

struct RunningTransfer {
    id: u64,
    child: CommandChild,
}

#[derive(Default)]
struct DesktopState {
    running: Mutex<Option<RunningTransfer>>,
    next_id: AtomicU64,
    active_id: AtomicU64,
    cancelled_id: AtomicU64,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct TransferUpdate {
    status: &'static str,
    message: String,
    code: Option<String>,
    file_name: Option<String>,
}

impl TransferUpdate {
    fn new(status: &'static str, message: impl Into<String>) -> Self {
        Self {
            status,
            message: message.into(),
            code: None,
            file_name: None,
        }
    }
}

#[derive(Clone, Copy)]
enum Direction {
    Send,
    Receive,
}

impl Direction {
    fn active_message(self) -> &'static str {
        match self {
            Self::Send => "Sending securely…",
            Self::Receive => "Receiving securely…",
        }
    }

    fn complete_message(self) -> &'static str {
        match self {
            Self::Send => "The file was sent successfully.",
            Self::Receive => "The file was received successfully.",
        }
    }
}

fn emit(app: &AppHandle, update: TransferUpdate) {
    let _ = app.emit("transfer-state", update);
}

fn file_path_to_string(path: FilePath) -> Option<String> {
    path.as_path().map(|path| path.to_string_lossy().into_owned())
}

#[tauri::command]
async fn choose_file(app: AppHandle) -> Result<Option<String>, String> {
    tauri::async_runtime::spawn_blocking(move || {
        app.dialog()
            .file()
            .blocking_pick_file()
            .and_then(file_path_to_string)
    })
    .await
    .map_err(|error| format!("Could not open the file picker: {error}"))
}

#[tauri::command]
async fn choose_directory(app: AppHandle) -> Result<Option<String>, String> {
    tauri::async_runtime::spawn_blocking(move || {
        app.dialog()
            .file()
            .blocking_pick_folder()
            .and_then(file_path_to_string)
    })
    .await
    .map_err(|error| format!("Could not open the folder picker: {error}"))
}

fn reserve_transfer(state: &Arc<DesktopState>) -> Result<u64, String> {
    let id = state.next_id.fetch_add(1, Ordering::Relaxed) + 1;
    if state
        .active_id
        .compare_exchange(0, id, Ordering::AcqRel, Ordering::Acquire)
        .is_err()
    {
        return Err("Another transfer is already running.".to_string());
    }
    state.cancelled_id.store(0, Ordering::Release);
    Ok(id)
}

fn release_reservation(state: &Arc<DesktopState>, id: u64) {
    let _ = state
        .active_id
        .compare_exchange(id, 0, Ordering::AcqRel, Ordering::Acquire);
}

fn store_child(
    state: &Arc<DesktopState>,
    id: u64,
    child: CommandChild,
) -> Result<(), String> {
    if state.active_id.load(Ordering::Acquire) != id {
        let _ = child.kill();
        return Err("The transfer was cancelled before it started.".to_string());
    }
    let mut running = match state.running.lock() {
        Ok(running) => running,
        Err(_) => {
            let _ = child.kill();
            release_reservation(state, id);
            return Err("The transfer state is unavailable.".to_string());
        }
    };
    if running.is_some() {
        let _ = child.kill();
        release_reservation(state, id);
        return Err("Another transfer is already running.".to_string());
    }
    *running = Some(RunningTransfer { id, child });
    Ok(())
}

fn clear_if_current(state: &Arc<DesktopState>, id: u64) -> bool {
    let Ok(mut running) = state.running.lock() else {
        return false;
    };
    if running.as_ref().is_some_and(|transfer| transfer.id == id) {
        *running = None;
        release_reservation(state, id);
        true
    } else {
        false
    }
}

fn parse_code(line: &str) -> Option<String> {
    line.trim()
        .strip_prefix("Code is:")
        .map(str::trim)
        .filter(|code| !code.is_empty())
        .map(str::to_owned)
}

fn is_transfer_started(line: &str) -> bool {
    line.contains("Sending (->") || line.contains("Receiving (<-")
}

fn safe_diagnostic_line(line: &str) -> Option<String> {
    let line = line.trim();
    if line.is_empty()
        || line.starts_with("Code is:")
        || line.contains("CROC_SECRET")
        || line.starts_with("On the other computer run:")
        || line.starts_with("croc ")
    {
        return None;
    }
    Some(line.chars().take(500).collect())
}

fn failure_message(lines: &VecDeque<String>, code: Option<i32>) -> String {
    let useful = lines
        .iter()
        .rev()
        .find(|line| {
            let lower = line.to_ascii_lowercase();
            lower.contains("error")
                || lower.contains("failed")
                || lower.contains("could not")
                || lower.contains("connection refused")
                || lower.contains("timed out")
        })
        .or_else(|| lines.back());

    useful.cloned().unwrap_or_else(|| match code {
        Some(code) => format!("croc stopped with exit code {code}."),
        None => "croc stopped before the transfer completed.".to_string(),
    })
}

fn watch_transfer(
    app: AppHandle,
    state: Arc<DesktopState>,
    id: u64,
    direction: Direction,
    mut receiver: tauri::async_runtime::Receiver<CommandEvent>,
) {
    tauri::async_runtime::spawn(async move {
        let mut diagnostics = VecDeque::with_capacity(MAX_DIAGNOSTIC_LINES);
        let mut announced_transfer = false;

        while let Some(event) = receiver.recv().await {
            match event {
                CommandEvent::Stdout(bytes) | CommandEvent::Stderr(bytes) => {
                    let text = String::from_utf8_lossy(&bytes);
                    for line in text.lines() {
                        if let Some(code) = parse_code(line) {
                            let mut update = TransferUpdate::new(
                                "waiting",
                                "Enter this code on your other device.",
                            );
                            update.code = Some(code);
                            emit(&app, update);
                        }

                        if !announced_transfer && is_transfer_started(line) {
                            announced_transfer = true;
                            emit(
                                &app,
                                TransferUpdate::new("transferring", direction.active_message()),
                            );
                        }

                        if let Some(line) = safe_diagnostic_line(line) {
                            if diagnostics.len() == MAX_DIAGNOSTIC_LINES {
                                diagnostics.pop_front();
                            }
                            diagnostics.push_back(line);
                        }
                    }
                }
                CommandEvent::Error(error) => {
                    if diagnostics.len() == MAX_DIAGNOSTIC_LINES {
                        diagnostics.pop_front();
                    }
                    diagnostics.push_back(error);
                }
                CommandEvent::Terminated(payload) => {
                    clear_if_current(&state, id);
                    if state.cancelled_id.load(Ordering::Acquire) == id {
                        emit(
                            &app,
                            TransferUpdate::new("cancelled", "No files were transferred."),
                        );
                    } else if payload.code == Some(0) {
                        emit(
                            &app,
                            TransferUpdate::new("completed", direction.complete_message()),
                        );
                    } else {
                        emit(
                            &app,
                            TransferUpdate::new(
                                "failed",
                                failure_message(&diagnostics, payload.code),
                            ),
                        );
                    }
                    break;
                }
                _ => {}
            }
        }
    });
}

#[tauri::command]
async fn send_file(
    app: AppHandle,
    state: State<'_, Arc<DesktopState>>,
    path: String,
) -> Result<(), String> {
    let file = Path::new(&path);
    if !file.is_file() {
        return Err("Choose a file that still exists on this computer.".to_string());
    }
    let file_name = file
        .file_name()
        .map(|name| name.to_string_lossy().into_owned())
        .unwrap_or_else(|| "file".to_string());
    let state = Arc::clone(state.inner());
    let id = reserve_transfer(&state)?;

    let mut preparing = TransferUpdate::new("preparing", format!("Preparing {file_name}…"));
    preparing.file_name = Some(file_name);
    emit(&app, preparing);

    let command = match app.shell().sidecar("croc") {
        Ok(command) => command,
        Err(error) => {
            release_reservation(&state, id);
            return Err(format!(
                "Could not locate the bundled croc program: {error}"
            ));
        }
    }
    .args([
        "--yes",
        "--ignore-stdin",
        "--disable-clipboard",
        "--relay",
        PUBLIC_RELAY,
        "send",
        &path,
    ]);

    let (receiver, child) = match command.spawn() {
        Ok(process) => process,
        Err(error) => {
            release_reservation(&state, id);
            return Err(format!("Could not start croc: {error}"));
        }
    };
    store_child(&state, id, child)?;
    watch_transfer(app, state, id, Direction::Send, receiver);
    Ok(())
}

#[tauri::command]
async fn receive_file(
    app: AppHandle,
    state: State<'_, Arc<DesktopState>>,
    code: String,
    directory: String,
) -> Result<(), String> {
    let code = code.trim().to_string();
    if code.len() < 6 || code.len() > 256 || code.chars().any(char::is_control) {
        return Err("Enter a valid croc code (at least 6 characters).".to_string());
    }
    if !Path::new(&directory).is_dir() {
        return Err("Choose a folder that still exists on this computer.".to_string());
    }
    let state = Arc::clone(state.inner());
    let id = reserve_transfer(&state)?;
    emit(
        &app,
        TransferUpdate::new("preparing", "Connecting to the other device…"),
    );

    let command = match app.shell().sidecar("croc") {
        Ok(command) => command,
        Err(error) => {
            release_reservation(&state, id);
            return Err(format!(
                "Could not locate the bundled croc program: {error}"
            ));
        }
    }
    .env("CROC_SECRET", code)
    .args([
        "--yes",
        "--overwrite",
        "--ignore-stdin",
        "--disable-clipboard",
        "--relay",
        PUBLIC_RELAY,
        "--out",
        &directory,
    ]);

    let (receiver, child) = match command.spawn() {
        Ok(process) => process,
        Err(error) => {
            release_reservation(&state, id);
            return Err(format!("Could not start croc: {error}"));
        }
    };
    store_child(&state, id, child)?;
    watch_transfer(app, state, id, Direction::Receive, receiver);
    Ok(())
}

#[tauri::command]
fn cancel_transfer(state: State<'_, Arc<DesktopState>>) -> Result<(), String> {
    let active_id = state.active_id.swap(0, Ordering::AcqRel);
    if active_id == 0 {
        return Ok(());
    }
    state.cancelled_id.store(active_id, Ordering::Release);

    let running = state
        .running
        .lock()
        .map_err(|_| "The transfer state is unavailable.".to_string())?
        .take();
    let Some(running) = running else {
        return Ok(());
    };

    running
        .child
        .kill()
        .map_err(|error| format!("Could not stop the transfer: {error}"))
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_shell::init())
        .manage(Arc::new(DesktopState::default()))
        .invoke_handler(tauri::generate_handler![
            choose_file,
            choose_directory,
            send_file,
            receive_file,
            cancel_transfer
        ])
        .run(tauri::generate_context!())
        .expect("error while running Croc Transfer");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extracts_pairing_code_without_prefix_whitespace() {
        assert_eq!(
            parse_code(" Code is: 4063-chicago-flute-select "),
            Some("4063-chicago-flute-select".to_string())
        );
    }

    #[test]
    fn pairing_code_is_not_kept_as_diagnostic_output() {
        assert_eq!(safe_diagnostic_line("Code is: secret-code"), None);
        assert_eq!(safe_diagnostic_line("CROC_SECRET=secret croc"), None);
        assert_eq!(safe_diagnostic_line("croc secret-code"), None);
    }

    #[test]
    fn recognizes_both_transfer_directions() {
        assert!(is_transfer_started("Sending (->1.2.3.4:9009)"));
        assert!(is_transfer_started("Receiving (<-1.2.3.4:9009)"));
        assert!(!is_transfer_started("connecting..."));
    }

    #[test]
    fn surfaces_the_most_useful_failure_line() {
        let lines = VecDeque::from([
            "connecting...".to_string(),
            "could not connect to relay".to_string(),
        ]);
        assert_eq!(
            failure_message(&lines, Some(1)),
            "could not connect to relay"
        );
    }
}
