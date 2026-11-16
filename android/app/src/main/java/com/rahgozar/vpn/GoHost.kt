package com.rahgozar.vpn

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.telephony.TelephonyManager
import android.util.Log
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import gocore.Gocore
import gocore.Host
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.Inet4Address
import java.net.NetworkInterface
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

/** Implements the Go Host interface: everything Go needs from Android. */
class GoHost(private val ctx: Context) : Host {

    private val cm = ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager

    private fun <T> onMain(block: () -> T): T? {
        if (android.os.Looper.myLooper() == android.os.Looper.getMainLooper()) return block()
        val ref = AtomicReference<T>()
        val latch = CountDownLatch(1)
        App.main.post {
            try { ref.set(block()) } finally { latch.countDown() }
        }
        latch.await(5, TimeUnit.SECONDS)
        return ref.get()
    }

    // ---------------- VPN ----------------

    override fun openTun(optionsJSON: String): Int {
        val svc = RahgozarVpnService.ensureRunning(ctx)
            ?: throw Exception("سرویس VPN اجرا نشد؛ اجازه VPN را بررسی کنید")
        return svc.openTun(JSONObject(optionsJSON))
    }

    override fun protect(fd: Int): Boolean {
        val svc = App.vpn ?: return true // our own app is outside the VPN anyway
        return svc.protect(fd)
    }

    override fun closeTun() {
        App.vpn?.stopTunnel()
    }

    // ---------------- UI bridge ----------------

    override fun emit(name: String, data: String) {
        if (name == "status") App.instance.onStatus(data)
        App.activity?.dispatchEvent(name, data)
    }

    override fun clipboardGet(): String = onMain {
        val cb = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        val clip = cb.primaryClip
        if (clip != null && clip.itemCount > 0) clip.getItemAt(0).coerceToText(ctx)?.toString() ?: "" else ""
    } ?: ""

    override fun clipboardSet(text: String) {
        onMain {
            val cb = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
            cb.setPrimaryClip(ClipData.newPlainText("Rahgozar", text))
        }
    }

    override fun pickFiles(mimeTypes: String, multiple: Boolean) {
        val act = App.activity
        if (act == null) {
            Gocore.filesPicked("[]")
            return
        }
        App.main.post { act.pickFiles(multiple) }
    }

    override fun openURL(url: String) {
        App.main.post {
            try {
                ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } catch (e: Exception) {
                Log.w("Rahgozar", "openURL", e)
            }
        }
    }

    override fun installAPK(path: String) {
        if (Build.VERSION.SDK_INT >= 26 && !ctx.packageManager.canRequestPackageInstalls()) {
            App.main.post {
                ctx.startActivity(
                    Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + ctx.packageName))
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                )
            }
            throw Exception("اجازه نصب برنامه را به رهگذر بدهید و دوباره «نصب» را بزنید")
        }
        val uri = FileProvider.getUriForFile(ctx, ctx.packageName + ".files", File(path))
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, "application/vnd.android.package-archive")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        App.main.post { ctx.startActivity(intent) }
    }

    // ---------------- network info ----------------

    override fun interfaces(): String {
        val out = JSONArray()
        for (n in cm.allNetworks) {
            val lp = cm.getLinkProperties(n) ?: continue
            val caps = cm.getNetworkCapabilities(n) ?: continue
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) continue
            val name = lp.interfaceName ?: continue
            val ni = try { NetworkInterface.getByName(name) } catch (e: Exception) { null } ?: continue
            val o = JSONObject()
            o.put("index", ni.index)
            o.put("mtu", try { ni.mtu } catch (e: Exception) { 1500 })
            o.put("name", name)
            o.put("addresses", JSONArray(lp.linkAddresses.map { it.address.hostAddress?.substringBefore('%') + "/" + it.prefixLength }))
            var flags = 0
            try {
                if (ni.isUp) flags = flags or 1
                if (ni.isLoopback) flags = flags or 8
                if (ni.isPointToPoint) flags = flags or 16
                if (ni.supportsMulticast()) flags = flags or 32
            } catch (_: Exception) {
            }
            o.put("flags", flags or 2) // broadcast
            o.put("type", when {
                caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> 0
                caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> 1
                caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> 2
                else -> 3
            })
            o.put("dns", JSONArray(lp.dnsServers.mapNotNull { it.hostAddress }))
            o.put("gateways", JSONArray(lp.routes.filter { it.hasGateway() }.mapNotNull { it.gateway?.hostAddress }))
            o.put("metered", !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED))
            out.put(o)
        }
        return out.toString()
    }

    /**
     * The physical network the device would use without our VPN. Once the
     * tunnel is up, cm.activeNetwork / the default-network callback return the
     * VPN itself (tun0); reporting that to sing-box makes it bind outbound
     * sockets to the tunnel and loop.
     */
    private fun underlying(exclude: Network? = null): Network? {
        fun ok(n: Network): Boolean {
            if (n == exclude) return false
            val c = cm.getNetworkCapabilities(n) ?: return false
            if (c.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) return false
            if (!c.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) return false
            return cm.getLinkProperties(n)?.interfaceName != null
        }
        cm.activeNetwork?.let { if (ok(it)) return it }
        val all = cm.allNetworks.filter { ok(it) }
        fun score(n: Network): Int {
            val c = cm.getNetworkCapabilities(n) ?: return 0
            var v = 0
            if (c.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)) v += 100
            if (c.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) v += 30
            if (c.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) v += 20
            if (c.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) v += 10
            return v
        }
        return all.maxByOrNull { score(it) }
    }

    override fun networkName(): String {
        val n = underlying() ?: return ""
        val caps = cm.getNetworkCapabilities(n) ?: return ""
        return when {
            caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> {
                val lp = cm.getLinkProperties(n)
                val gw = lp?.routes?.firstOrNull { it.hasGateway() && it.gateway is Inet4Address }?.gateway?.hostAddress ?: ""
                "Wi-Fi $gw"
            }
            caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> {
                val tm = ctx.getSystemService(Context.TELEPHONY_SERVICE) as TelephonyManager
                "Mobile " + (tm.networkOperatorName ?: "")
            }
            caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "Ethernet"
            else -> "Network"
        }.trim()
    }

    // ---------------- default (physical) network monitor ----------------

    private var callback: ConnectivityManager.NetworkCallback? = null
    @Volatile private var lastReport = "?"

    private fun refresh(gone: Network? = null) {
        val n = underlying(gone)
        if (n == null) {
            if (lastReport != "") { lastReport = ""; Gocore.updateDefaultInterface("", -1, false, false) }
            return
        }
        val lp: LinkProperties = cm.getLinkProperties(n) ?: return
        val caps = cm.getNetworkCapabilities(n)
        val name = lp.interfaceName ?: return
        val idx = try { NetworkInterface.getByName(name)?.index ?: -1 } catch (e: Exception) { -1 }
        val metered = caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) == false
        val key = "$name/$idx/$metered"
        if (key == lastReport) return
        lastReport = key
        Gocore.updateDefaultInterface(name, idx, metered, false)
    }

    override fun startNetworkMonitor() {
        if (callback != null) return
        lastReport = "?"
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = refresh()
            override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) = refresh()
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) = refresh()
            override fun onLost(network: Network) = refresh(network)
        }
        callback = cb
        // all non-VPN internet networks (NetworkRequest excludes VPNs by default)
        val req = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .build()
        try {
            if (Build.VERSION.SDK_INT >= 26) cm.registerNetworkCallback(req, cb, App.main)
            else cm.registerNetworkCallback(req, cb)
        } catch (e: Exception) {
            Log.w("Rahgozar", "network callback", e)
        }
        App.io.execute { refresh() }
    }

    override fun stopNetworkMonitor() {
        val cb = callback ?: return
        callback = null
        try { cm.unregisterNetworkCallback(cb) } catch (_: Exception) {}
    }

    override fun log(line: String) {
        Log.i("Rahgozar", line)
    }

    @Suppress("unused")
    private fun startService(i: Intent) = ContextCompat.startForegroundService(ctx, i)
}
