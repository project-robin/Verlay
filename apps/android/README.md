# Verlay Android beta

This app provides the paired-device Verlay experience on Android. It builds and
packages the Verlay runtime into an ARM64 APK.

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

The APK now also packages `libverlay.so`, the same Go runtime used by
`verlay` and `verlayd` on Linux.
1. Enable USB debugging, connect the phone, and install the APK with
   `adb install -r app/build/outputs/apk/debug/app-debug.apk`.
2. Tap **Make phone available**.
3. Create a four-word code on one device and enter it on the other.
4. Send a file in each direction and compare SHA-256 hashes.

From the app you can:

- create or enter a four-word pairing code;
- send a selected document to a paired Linux device;
- automatically receive into a paired device's bounded inbox.

Incoming mesh files are first received in app-private storage and then
published to `Downloads/Verlay` through MediaStore. The persistent identity seed
is wrapped by Android Keystore.

The Gradle build shares its Go compilation cache through `GOCACHE` (default
`/tmp/croc-android-go-cache`) to avoid another large cache inside the project.
