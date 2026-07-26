import org.gradle.api.tasks.Exec

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

val repositoryRoot = rootProject.projectDir.parentFile.parentFile
val generatedJniRoot = layout.buildDirectory.dir("generated/croc-jni")
val crocBinary = generatedJniRoot.map { it.file("arm64-v8a/libcroc.so") }
val meshBinary = generatedJniRoot.map { it.file("arm64-v8a/libmesh.so") }
val sharedGoBuildCache = System.getenv("GOCACHE")
    ?: File(System.getProperty("java.io.tmpdir"), "croc-android-go-cache").absolutePath

val buildCrocAndroid by tasks.registering(Exec::class) {
    group = "build"
    description = "Cross-compile this checkout of croc for Android ARM64"
    workingDir(repositoryRoot)
    environment("CGO_ENABLED", "0")
    environment("GOOS", "android")
    environment("GOARCH", "arm64")
    environment("GOCACHE", sharedGoBuildCache)
    commandLine(
        "go",
        "build",
        "-trimpath",
        "-ldflags=-s -w",
        "-o",
        crocBinary.get().asFile.absolutePath,
        ".",
    )
    inputs.files(repositoryRoot.resolve("go.mod"), repositoryRoot.resolve("go.sum"))
    inputs.files(repositoryRoot.resolve("main.go"))
    inputs.dir(repositoryRoot.resolve("src"))
    outputs.file(crocBinary)
    doFirst {
        crocBinary.get().asFile.parentFile.mkdirs()
    }
}

val buildMeshAndroid by tasks.registering(Exec::class) {
    group = "build"
    description = "Cross-compile the daemon-centered mesh CLI for Android ARM64"
    workingDir(repositoryRoot)
    environment("CGO_ENABLED", "0")
    environment("GOOS", "android")
    environment("GOARCH", "arm64")
    environment("GOCACHE", sharedGoBuildCache)
    commandLine(
        "go",
        "build",
        "-trimpath",
        "-ldflags=-s -w",
        "-o",
        meshBinary.get().asFile.absolutePath,
        "./cmd/mesh",
    )
    inputs.files(repositoryRoot.resolve("go.mod"), repositoryRoot.resolve("go.sum"))
    inputs.dir(repositoryRoot.resolve("cmd/mesh"))
    inputs.dir(repositoryRoot.resolve("internal/mesh"))
    inputs.dir(repositoryRoot.resolve("src"))
    outputs.file(meshBinary)
    doFirst {
        meshBinary.get().asFile.parentFile.mkdirs()
    }
}

android {
    namespace = "com.croc.prototype"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.croc.prototype"
        minSdk = 26
        targetSdk = 37
        versionCode = 1
        versionName = "0.1.0"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        ndk {
            abiFilters += "arm64-v8a"
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }

    sourceSets {
        getByName("main").jniLibs.setSrcDirs(listOf(generatedJniRoot.get().asFile))
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }
}

tasks.named("preBuild").configure {
    dependsOn(buildCrocAndroid)
    dependsOn(buildMeshAndroid)
}

dependencies {
    val composeBom = platform("androidx.compose:compose-bom:2026.06.00")

    implementation(composeBom)
    androidTestImplementation(composeBom)

    implementation("androidx.activity:activity-compose:1.13.0")
    implementation("androidx.core:core-ktx:1.19.0")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.10.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.10.2")

    debugImplementation("androidx.compose.ui:ui-tooling")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlin:kotlin-test:2.4.10")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.7.0")
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
