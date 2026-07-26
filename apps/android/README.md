# Verlay Android beta

This app provides the paired-device Verlay experience on Android and retains a
legacy Croc code-transfer mode for compatibility testing. It builds the Go
source from the repository root and packages the Verlay runtime into an ARM64 APK.

## Requirements

- Android SDK Platform 37.0 and Build Tools 36.0.0
- JDK 17 or newer
- Go 1.25 or newer
- An ARM64 phone running Android 8.0 or newer

## Build and test

```sh
./gradlew testDebugUnitTest assembleDebug
```

For low-memory machines, use `./build-low-memory.sh`. It automatically prefers
an explicit `JAVA_HOME`, then the system JDK, and uses the checked-in Gradle
Wrapper when the temporary Gradle distribution is unavailable after reboot.

The APK is written to `app/build/outputs/apk/debug/app-debug.apk`.

## Physical-device smoke test

1. Enable USB debugging, connect the phone, and run `adb devices`.
2. Install with `adb install -r app/build/outputs/apk/debug/app-debug.apk`.
3. Linux to Android: run `croc send --no-local FILE`, enter the shown code in the app, and
   compare SHA-256 hashes after the file appears in `Downloads/Croc`.
4. Android to Linux: choose a file in the app, then run `CROC_SECRET='CODE' croc` on Linux and
   compare SHA-256 hashes.

Both devices need outbound internet access to the default public croc relay. The prototype
supports one regular file per transfer; folders and multi-file batches are intentionally out of
scope.

## Verlay paired-device mode

The APK now also packages `libverlay.so`, the same Go runtime used by
`verlay` and `verlayd` on Linux.
Tap **Make phone available** to run it inside a visible Android foreground
service. From that section you can:

- create or enter a four-word pairing code;
- compare and approve the other device's fingerprint;
- send a selected document to a paired Linux device;
- approve or reject incoming paired-device transfers.

Incoming mesh files are first received in app-private storage and then
published to `Downloads/Verlay` through MediaStore. The persistent identity seed
is wrapped by Android Keystore. The original Croc code-based workflow remains
on the same screen as a compatibility fallback.

The Gradle build shares its Go compilation cache through `GOCACHE` (default
`/tmp/croc-android-go-cache`) so the two embedded binaries do not create another
large cache inside the project.
