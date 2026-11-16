package com.rahgozar.vpn

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.ComponentName
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.service.quicksettings.TileService as SysTileService
import android.util.Log
import gocore.Gocore
import org.json.JSONObject
import java.util.concurrent.Executors

class App : Application() {
    companion object {
        lateinit var instance: App
            private set
        @Volatile var activity: MainActivity? = null
        @Volatile var vpn: RahgozarVpnService? = null
        @Volatile var lastStatus: JSONObject = JSONObject()
        const val CHANNEL = "status"
        val io = Executors.newCachedThreadPool()
        val main = Handler(Looper.getMainLooper())
    }

    lateinit var host: GoHost
        private set

    override fun onCreate() {
        super.onCreate()
        instance = this
        if (Build.VERSION.SDK_INT >= 26) {
            val nm = getSystemService(NotificationManager::class.java)
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, getString(R.string.notif_channel), NotificationManager.IMPORTANCE_LOW)
            )
        }
        host = GoHost(this)
        val version = try {
            packageManager.getPackageInfo(packageName, 0).versionName ?: "1.0.0"
        } catch (e: Exception) { "1.0.0" }
        try {
            Gocore.setup(filesDir.absolutePath, cacheDir.absolutePath, packageName, version, host)
        } catch (e: Exception) {
            Log.e("Rahgozar", "setup failed", e)
        }
    }

    /** Called for every "status" event from Go. */
    fun onStatus(json: String) {
        val st = try { JSONObject(json) } catch (e: Exception) { return }
        lastStatus = st
        val state = st.optString("state")
        vpn?.updateNotification(st)
        if (state == "connected" && vpn == null) {
            // proxy mode has no TUN, but still needs a foreground service to stay alive
            io.execute { RahgozarVpnService.ensureRunning(this) }
        }
        if (state == "idle" || state == "error") {
            vpn?.stopTunnel()
        }
        try {
            SysTileService.requestListeningState(this, ComponentName(this, TileService::class.java))
        } catch (_: Exception) {
        }
    }
}
