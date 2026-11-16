package com.rahgozar.vpn

import android.app.Notification
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ProxyInfo
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import gocore.Gocore
import org.json.JSONObject
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * Foreground VPN service. The Go core asks for the TUN interface through
 * GoHost.openTun; this service builds it with VpnService.Builder.
 */
class RahgozarVpnService : VpnService() {

    companion object {
        const val ACTION_START = "com.rahgozar.vpn.START"
        const val ACTION_STOP = "com.rahgozar.vpn.STOP"
        private const val NOTIF_ID = 1
        @Volatile private var ready = CountDownLatch(1)

        /** Starts the service (foreground) and waits until it is ready. */
        fun ensureRunning(ctx: Context): RahgozarVpnService? {
            App.vpn?.let { return it }
            if (prepare(ctx) != null) return null // permission not granted
            ready = CountDownLatch(1)
            val i = Intent(ctx, RahgozarVpnService::class.java).setAction(ACTION_START)
            App.main.post { ContextCompat.startForegroundService(ctx, i) }
            ready.await(10, TimeUnit.SECONDS)
            return App.vpn
        }
    }

    private var tun: ParcelFileDescriptor? = null

    override fun onCreate() {
        super.onCreate()
        App.vpn = this
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            Gocore.disconnect()
            stopTunnel()
            return START_NOT_STICKY
        }
        val n = buildNotification(App.lastStatus)
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIF_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIF_ID, n)
        }
        App.vpn = this
        ready.countDown()
        return START_NOT_STICKY
    }

    /** Builds the TUN interface from sing-box's options; returns the fd. */
    fun openTun(o: JSONObject): Int {
        val b = Builder()
            .setSession("Rahgozar")
            .setMtu(o.optInt("mtu", 9000).coerceIn(1280, 65535))
        if (Build.VERSION.SDK_INT >= 29) b.setMetered(false)

        val v4 = o.optJSONArray("inet4Address")
        val v6 = o.optJSONArray("inet6Address")
        for (i in 0 until (v4?.length() ?: 0)) {
            val p = v4!!.getJSONObject(i)
            b.addAddress(p.getString("address"), p.getInt("prefix"))
        }
        for (i in 0 until (v6?.length() ?: 0)) {
            val p = v6!!.getJSONObject(i)
            b.addAddress(p.getString("address"), p.getInt("prefix"))
        }
        if (o.optBoolean("autoRoute", true)) {
            val dns = o.optJSONArray("dnsServers")
            for (i in 0 until (dns?.length() ?: 0)) {
                try { b.addDnsServer(dns!!.getString(i)) } catch (_: Exception) {}
            }
            val r4 = o.optJSONArray("inet4Route")
            if (r4 == null || r4.length() == 0) {
                if ((v4?.length() ?: 0) > 0) b.addRoute("0.0.0.0", 0)
            } else {
                for (i in 0 until r4.length()) {
                    val p = r4.getJSONObject(i)
                    b.addRoute(p.getString("address"), p.getInt("prefix"))
                }
            }
            val r6 = o.optJSONArray("inet6Route")
            if (r6 == null || r6.length() == 0) {
                if ((v6?.length() ?: 0) > 0) b.addRoute("::", 0)
            } else {
                for (i in 0 until r6.length()) {
                    val p = r6.getJSONObject(i)
                    b.addRoute(p.getString("address"), p.getInt("prefix"))
                }
            }
        }
        val include = o.optJSONArray("includePackage")
        val exclude = o.optJSONArray("excludePackage")
        if (include != null && include.length() > 0) {
            for (i in 0 until include.length()) {
                try { b.addAllowedApplication(include.getString(i)) } catch (_: Exception) {}
            }
        } else {
            // never route our own traffic (probes, in-process engines) into the tunnel
            try { b.addDisallowedApplication(packageName) } catch (_: Exception) {}
            for (i in 0 until (exclude?.length() ?: 0)) {
                val pkg = exclude!!.getString(i)
                if (pkg == packageName) continue
                try { b.addDisallowedApplication(pkg) } catch (_: Exception) {}
            }
        }
        if (Build.VERSION.SDK_INT >= 29 && o.optBoolean("httpProxy")) {
            val bypass = o.optJSONArray("proxyBypass")
            val list = (0 until (bypass?.length() ?: 0)).map { bypass!!.getString(it) }
            b.setHttpProxy(ProxyInfo.buildDirectProxy(o.getString("proxyHost"), o.getInt("proxyPort"), list))
        }
        val act = App.activity
        if (act != null) {
            b.setConfigureIntent(
                PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
            )
        }
        tun?.close()
        val pfd = b.establish() ?: throw Exception("اجازه VPN داده نشده است")
        tun = pfd
        return pfd.fd
    }

    fun stopTunnel() {
        try { tun?.close() } catch (_: Exception) {}
        tun = null
        if (Build.VERSION.SDK_INT >= 24) stopForeground(STOP_FOREGROUND_REMOVE) else @Suppress("DEPRECATION") stopForeground(true)
        stopSelf()
    }

    override fun onRevoke() {
        Log.i("Rahgozar", "VPN revoked")
        Gocore.disconnect()
        stopTunnel()
    }

    override fun onDestroy() {
        try { tun?.close() } catch (_: Exception) {}
        tun = null
        if (App.vpn === this) App.vpn = null
        super.onDestroy()
    }

    private fun buildNotification(st: JSONObject): Notification {
        val state = st.optString("state", "connecting")
        val title = when (state) {
            "connected" -> "متصل به " + st.optString("profileName")
            "connecting" -> "در حال اتصال…"
            "error" -> "خطا در اتصال"
            else -> "رهگذر"
        }
        val lat = st.optInt("latency", 0)
        val text = if (state == "connected") {
            "↓ " + speed(st.optLong("downSpeed")) + "   ↑ " + speed(st.optLong("upSpeed")) + (if (lat > 0) "   $lat ms" else "")
        } else st.optString("message", "")
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, RahgozarVpnService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
        return NotificationCompat.Builder(this, App.CHANNEL)
            .setSmallIcon(R.drawable.ic_tile)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setColor(0xFF35C9B5.toInt())
            .addAction(0, "قطع اتصال", stop)
            .build()
    }

    private var lastNotify = 0L

    fun updateNotification(st: JSONObject) {
        val now = System.currentTimeMillis()
        if (now - lastNotify < 900 && st.optString("state") == "connected") return
        lastNotify = now
        try {
            val nm = getSystemService(android.app.NotificationManager::class.java)
            nm.notify(NOTIF_ID, buildNotification(st))
        } catch (_: Exception) {
        }
    }

    private fun speed(b: Long): String {
        var v = b.toDouble()
        val u = arrayOf("B/s", "KB/s", "MB/s", "GB/s")
        var i = 0
        while (v >= 1024 && i < u.size - 1) { v /= 1024; i++ }
        return if (i == 0) "${v.toInt()} ${u[i]}" else String.format("%.1f %s", v, u[i])
    }
}
