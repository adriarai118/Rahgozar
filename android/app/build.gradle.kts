plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val appVersion = (System.getenv("RG_VERSION") ?: "1.0.0").trim()
val parts = appVersion.split(".").map { it.toIntOrNull() ?: 0 } + listOf(0, 0, 0)
val code = parts[0] * 10000 + parts[1] * 100 + parts[2]

android {
    namespace = "com.rahgozar.vpn"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.rahgozar.vpn"
        minSdk = 24
        targetSdk = 35
        versionCode = code
        versionName = appVersion
    }

    signingConfigs {
        create("release") {
            val ks = System.getenv("RG_KEYSTORE")
            if (ks != null && file(ks).exists()) {
                storeFile = file(ks)
                storePassword = System.getenv("RG_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("RG_KEY_ALIAS") ?: "rahgozar"
                keyPassword = System.getenv("RG_KEYSTORE_PASSWORD")
            }
            enableV1Signing = true
            enableV2Signing = true
            enableV3Signing = true
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            val rel = signingConfigs.getByName("release")
            signingConfig = if (rel.storeFile != null) rel else signingConfigs.getByName("debug")
        }
    }

    splits {
        abi {
            isEnable = true
            reset()
            include("arm64-v8a", "armeabi-v7a")
            isUniversalApk = true
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }

    packaging {
        jniLibs { useLegacyPackaging = true }
    }
}

dependencies {
    implementation(fileTree(mapOf("dir" to "libs", "include" to listOf("*.aar"))))
    implementation("androidx.core:core-ktx:1.13.1")
}
