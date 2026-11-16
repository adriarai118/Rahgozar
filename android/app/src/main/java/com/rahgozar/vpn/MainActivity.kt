package com.rahgozar.vpn

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.provider.OpenableColumns
import android.view.View
import android.webkit.ConsoleMessage
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import gocore.Gocore
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

class MainActivity : Activity() {

    private lateinit var web: WebView
    private var pageReady = false
    private var pendingConnect: Int? = null
    private val pendingShare = mutableListOf<String>()

    companion object {
        private const val REQ_VPN = 10
        private const val REQ_FILES = 11
        private const val REQ_NOTIF = 12
    }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.statusBarColor = Color.parseColor("#0D1826")
        window.navigationBarColor = Color.parseColor("#0D1826")
        web = WebView(this)
        web.setBackgroundColor(Color.parseColor("#0D1826"))
        web.overScrollMode = View.OVER_SCROLL_NEVER
        with(web.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = true
            @Suppress("DEPRECATION")
            allowFileAccessFromFileURLs = true
            textZoom = 100
        }
        web.addJavascriptInterface(Bridge(), "RahgozarAndroid")
        web.webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView?, url: String?) {
                pageReady = true
                App.io.execute { Gocore.start() }
                flushShares()
            }

            override fun shouldOverrideUrlLoading(view: WebView?, url: String?): Boolean {
                if (url != null && !url.startsWith("file:")) {
                    try { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) } catch (_: Exception) {}
                    return true
                }
                return false
            }
        }
        web.webChromeClient = object : WebChromeClient() {
            override fun onConsoleMessage(m: ConsoleMessage): Boolean {
                android.util.Log.d("RahgozarUI", "${m.message()} @${m.lineNumber()}")
                return true
            }
        }
        setContentView(web)
        App.activity = this
        web.loadUrl("file:///android_asset/web/index.html")
        handleIntent(intent)
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQ_NOTIF)
        }
    }

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    override fun onResume() {
        super.onResume()
        App.activity = this
        if (pageReady) dispatchEvent("resume", "null")
    }

    override fun onDestroy() {
        if (App.activity === this) App.activity = null
        web.destroy()
        super.onDestroy()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        web.evaluateJavascript("window.__rgBack ? window.__rgBack() : false") { r ->
            if (r != "true") moveTaskToBack(true)
        }
    }

    // ---------------- share / deep links ----------------

    private fun handleIntent(i: Intent?) {
        if (i == null) return
        val text = when (i.action) {
            Intent.ACTION_SEND -> i.getStringExtra(Intent.EXTRA_TEXT)
            Intent.ACTION_VIEW -> i.dataString
            else -> null
        }
        if (!text.isNullOrBlank()) {
            pendingShare.add(text)
            if (pageReady) flushShares()
        }
        if (i.getBooleanExtra("connect", false)) {
            App.main.postDelayed({ requestConnect(null) }, 600)
        }
    }

    private fun flushShares() {
        val list = pendingShare.toList()
        pendingShare.clear()
        for (t in list) {
            App.io.execute {
                val r = Gocore.importText(t)
                runOnUiThread { dispatchEvent("imported", r) }
            }
        }
    }

    // ---------------- JS bridge ----------------

    fun dispatchEvent(name: String, data: String) {
        val js = "window.__rgEvent && window.__rgEvent(${JSONObject.quote(name)}, ${JSONObject.quote(data)})"
        runOnUiThread { if (!isFinishing) web.evaluateJavascript(js, null) }
    }

    private fun resolve(id: Int, json: String) {
        val js = "window.__rgResolve && window.__rgResolve($id, ${JSONObject.quote(json)})"
        runOnUiThread { if (!isFinishing) web.evaluateJavascript(js, null) }
    }

    inner class Bridge {
        @JavascriptInterface
        fun call(method: String, args: String, id: Int) {
            if (method == "Connect") {
                runOnUiThread { requestConnect(id) }
                return
            }
            if (method == "AndroidApps") {
                App.io.execute { resolve(id, JSONObject().put("result", installedApps()).toString()) }
                return
            }
            App.io.execute { resolve(id, Gocore.call(method, args)) }
        }

        @JavascriptInterface
        fun platform(): String = "android"
    }

    /** VPN permission first, then connect. */
    private fun requestConnect(id: Int?) {
        val intent = VpnService.prepare(this)
        if (intent != null) {
            pendingConnect = id ?: -1
            try {
                startActivityForResult(intent, REQ_VPN)
            } catch (e: Exception) {
                if (id != null) resolve(id, JSONObject().put("error", "باز کردن مجوز VPN ممکن نشد").toString())
            }
            return
        }
        App.io.execute {
            val r = Gocore.call("Connect", "[]")
            if (id != null && id >= 0) resolve(id, r)
        }
    }

    // ---------------- file picker ----------------

    fun pickFiles(multiple: Boolean) {
        val i = Intent(Intent.ACTION_OPEN_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .setType("*/*")
            .putExtra(Intent.EXTRA_ALLOW_MULTIPLE, multiple)
        try {
            startActivityForResult(i, REQ_FILES)
        } catch (e: Exception) {
            Gocore.filesPicked("[]")
        }
    }

    private fun copyToCache(uri: Uri): String? {
        var name = "import"
        try {
            contentResolver.query(uri, null, null, null, null)?.use { c ->
                val idx = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                if (idx >= 0 && c.moveToFirst()) name = c.getString(idx) ?: name
            }
        } catch (_: Exception) {
        }
        name = name.replace(Regex("[\\\\/:*?\"<>|]"), "_")
        val dir = File(cacheDir, "import").apply { mkdirs() }
        val out = File(dir, name)
        return try {
            contentResolver.openInputStream(uri)?.use { input -> out.outputStream().use { input.copyTo(it) } }
            out.absolutePath
        } catch (e: Exception) {
            null
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        when (requestCode) {
            REQ_VPN -> {
                val id = pendingConnect ?: -1
                pendingConnect = null
                if (resultCode == RESULT_OK) {
                    App.io.execute {
                        val r = Gocore.call("Connect", "[]")
                        if (id >= 0) resolve(id, r)
                    }
                } else if (id >= 0) {
                    resolve(id, JSONObject().put("error", "اجازه ساخت VPN داده نشد").toString())
                }
            }
            REQ_FILES -> {
                val uris = mutableListOf<Uri>()
                if (resultCode == RESULT_OK && data != null) {
                    data.clipData?.let { cd -> for (i in 0 until cd.itemCount) uris.add(cd.getItemAt(i).uri) }
                    if (uris.isEmpty()) data.data?.let { uris.add(it) }
                }
                App.io.execute {
                    val paths = JSONArray()
                    for (u in uris) copyToCache(u)?.let { paths.put(it) }
                    Gocore.filesPicked(paths.toString())
                }
            }
        }
    }

    // ---------------- installed apps (per-app tunnel) ----------------

    private fun installedApps(): JSONArray {
        val pm = packageManager
        val out = JSONArray()
        @Suppress("DEPRECATION")
        val list = pm.getInstalledApplications(PackageManager.GET_META_DATA)
        for (ai in list) {
            if (ai.packageName == packageName) continue
            val hasNet = try {
                pm.checkPermission(Manifest.permission.INTERNET, ai.packageName) == PackageManager.PERMISSION_GRANTED
            } catch (e: Exception) { true }
            if (!hasNet) continue
            val sys = (ai.flags and android.content.pm.ApplicationInfo.FLAG_SYSTEM) != 0
            out.put(JSONObject()
                .put("pkg", ai.packageName)
                .put("name", pm.getApplicationLabel(ai).toString())
                .put("system", sys))
        }
        return out
    }
}
