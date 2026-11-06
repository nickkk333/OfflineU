package com.nickkk333.offlineu;

import android.content.Context;
import android.content.SharedPreferences;
import android.net.Uri;
import android.text.TextUtils;

import java.util.regex.Pattern;

/**
 * Stores the OfflineU server address in SharedPreferences and normalizes what
 * the user typed. The app itself has no accounts: the address simply points the
 * WebView at the server that owns courses, sources and progress.
 */
final class ServerConfig {

    private static final String PREFS_NAME = "offlineu";
    private static final String KEY_SERVER_URL = "server_url";
    private static final Pattern HAS_SCHEME =
            Pattern.compile("^[a-zA-Z][a-zA-Z0-9+.-]*://");

    private ServerConfig() {
    }

    static String normalize(String raw) {
        if (raw == null) {
            return "";
        }
        String value = raw.trim();
        if (value.isEmpty()) {
            return "";
        }
        // A user typing "192.168.1.10:5000" means http, not a custom scheme.
        value = value.replaceAll("\\s+", "");
        if (!HAS_SCHEME.matcher(value).find()) {
            value = "http://" + value;
        }
        try {
            // A pasted page URL (for example ".../#/lesson/..." or "...?x=1")
            // still means the same server; keep only scheme, host and path.
            value = Uri.parse(value).buildUpon().fragment(null).query(null).build().toString();
        } catch (Exception ignored) {
            // isValid() reports the malformed value instead.
        }
        while (value.endsWith("/")) {
            value = value.substring(0, value.length() - 1);
        }
        return value;
    }

    static boolean isValid(String value) {
        if (TextUtils.isEmpty(value)) {
            return false;
        }
        try {
            Uri uri = Uri.parse(value);
            String scheme = uri.getScheme();
            if (scheme == null) {
                return false;
            }
            if (!scheme.equalsIgnoreCase("http") && !scheme.equalsIgnoreCase("https")) {
                return false;
            }
            return !TextUtils.isEmpty(uri.getHost());
        } catch (Exception ignored) {
            return false;
        }
    }

    static String load(Context context) {
        return preferences(context).getString(KEY_SERVER_URL, "");
    }

    static void save(Context context, String url) {
        preferences(context).edit().putString(KEY_SERVER_URL, url).apply();
    }

    static void clear(Context context) {
        preferences(context).edit().remove(KEY_SERVER_URL).apply();
    }

    /** Human readable address for the toolbar subtitle, without the scheme. */
    static String displayAddress(String url) {
        Uri uri = Uri.parse(url);
        String host = uri.getHost();
        if (host == null) {
            return url;
        }
        int port = uri.getPort();
        return port > 0 ? host + ":" + port : host;
    }

    private static SharedPreferences preferences(Context context) {
        return context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
    }
}
