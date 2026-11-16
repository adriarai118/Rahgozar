package com.rahgozar.vpn

import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.service.quicksettings.Tile
import gocore.Gocore
import org.json.JSONObject

/** Quick settings tile: one tap to connect / disconnect. */
class TileService : android.service.quicksettings.TileService() {

    override fun onStartListening() {
        super.onStartListening()
        refresh()
    }

    private fun refresh() {
        val tile = qsTile ?: return
        val st = try { JSONObject(Gocore.statusJSON()) } catch (e: Exception) { JSONObject() }
        val state = st.optString("state")
        tile.state = when (state) {
            "connected" -> Tile.STATE_ACTIVE
            "connecting" -> Tile.STATE_ACTIVE
            else -> Tile.STATE_INACTIVE
        }
        tile.label = "رهگذر"
        if (Build.VERSION.SDK_INT >= 29) {
            tile.subtitle = when (state) {
                "connected" -> st.optString("profileName")
                "connecting" -> "در حال اتصال…"
                else -> "قطع"
            }
        }
        tile.updateTile()
    }

    override fun onClick() {
        super.onClick()
        val st = try { JSONObject(Gocore.statusJSON()).optString("state") } catch (e: Exception) { "" }
        val connecting = st != "connected" && st != "connecting"
        if (connecting && VpnService.prepare(this) != null) {
            // needs the VPN permission dialog: open the app and connect there
            val i = Intent(this, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                .putExtra("connect", true)
            if (Build.VERSION.SDK_INT >= 34) {
                startActivityAndCollapse(
                    android.app.PendingIntent.getActivity(this, 0, i, android.app.PendingIntent.FLAG_IMMUTABLE)
                )
            } else {
                @Suppress("DEPRECATION")
                startActivityAndCollapse(i)
            }
            return
        }
        Gocore.toggle()
        App.main.postDelayed({ refresh() }, 800)
    }
}
