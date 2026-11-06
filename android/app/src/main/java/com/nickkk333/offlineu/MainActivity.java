package com.nickkk333.offlineu;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.graphics.Bitmap;
import android.net.Uri;
import android.net.http.SslError;
import android.os.Bundle;
import android.text.TextUtils;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowManager;
import android.webkit.CookieManager;
import android.webkit.SslErrorHandler;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.ProgressBar;
import android.widget.TextView;
import android.widget.Toast;
import android.widget.Toolbar;

import java.util.Locale;

/**
 * The whole UI is the OfflineU web application inside a WebView. Everything
 * (course picker, remote source manager, player, progress) is the same Vue app
 * the desktop browser loads, so there is no second implementation to maintain.
 */
public class MainActivity extends Activity {

    static final String EXTRA_SERVER_URL = "server_url";

    private static final int REQUEST_CHANGE_SERVER = 1001;
    private static final int REQUEST_FILE_CHOOSER = 1002;

    private static final String STATE_URL = "offlineu_url";
    private static final String STATE_ERROR_VISIBLE = "offlineu_error_visible";
    private static final String STATE_ERROR_TEXT = "offlineu_error_text";

    private WebView webView;
    private ProgressBar progress;
    private View errorPanel;
    private TextView errorMessage;
    private Toolbar toolbar;
    private FrameLayout fullscreenContainer;

    private String serverUrl;
    private View customView;
    private WebChromeClient.CustomViewCallback customViewCallback;
    private ValueCallback<Uri[]> filePathCallback;
    private long lastBackPress;

    @SuppressLint("SetJavaScriptEnabled")
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        serverUrl = getIntent().getStringExtra(EXTRA_SERVER_URL);
        if (TextUtils.isEmpty(serverUrl)) {
            serverUrl = ServerConfig.load(this);
        }
        if (savedInstanceState != null && !TextUtils.isEmpty(savedInstanceState.getString(STATE_URL))) {
            serverUrl = savedInstanceState.getString(STATE_URL);
        }
        if (!ServerConfig.isValid(serverUrl)) {
            startActivity(new Intent(this, LoginActivity.class));
            finish();
            return;
        }

        setContentView(R.layout.activity_main);

        toolbar = findViewById(R.id.toolbar);
        webView = findViewById(R.id.webview);
        progress = findViewById(R.id.progress);
        errorPanel = findViewById(R.id.error_panel);
        errorMessage = findViewById(R.id.error_message);
        fullscreenContainer = findViewById(R.id.fullscreen_container);

        toolbar.setTitle(R.string.app_name);
        updateToolbarSubtitle();
        toolbar.inflateMenu(R.menu.menu_main);
        toolbar.setOnMenuItemClickListener(item -> {
            int itemId = item.getItemId();
            if (itemId == R.id.action_reload) {
                hideError();
                webView.reload();
                return true;
            }
            if (itemId == R.id.action_open_browser) {
                openExternal(Uri.parse(currentUrl()));
                return true;
            }
            if (itemId == R.id.action_change_server) {
                changeServer();
                return true;
            }
            if (itemId == R.id.action_clear_data) {
                confirmClearSiteData();
                return true;
            }
            return false;
        });

        findViewById(R.id.retry_button).setOnClickListener(view -> retry());
        findViewById(R.id.change_server_button).setOnClickListener(view -> changeServer());

        configureWebView();

        if (savedInstanceState != null) {
            if (savedInstanceState.getBoolean(STATE_ERROR_VISIBLE, false)) {
                showError(savedInstanceState.getString(STATE_ERROR_TEXT, ""));
            }
            if (webView.restoreState(savedInstanceState) == null) {
                loadServer();
            }
        } else {
            loadServer();
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void configureWebView() {
        WebSettings settings = webView.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setDatabaseEnabled(true);
        // Course videos and audio should start without an extra tap.
        settings.setMediaPlaybackRequiresUserGesture(false);
        settings.setLoadWithOverviewMode(true);
        settings.setUseWideViewPort(true);
        settings.setSupportZoom(false);
        settings.setBuiltInZoomControls(false);
        settings.setDisplayZoomControls(false);
        settings.setAllowFileAccess(false);
        settings.setAllowContentAccess(false);
        settings.setJavaScriptCanOpenWindowsAutomatically(false);
        settings.setSupportMultipleWindows(false);
        settings.setCacheMode(WebSettings.LOAD_DEFAULT);
        // The frontend is served by the same LAN server; mixed content can only
        // appear if the page is loaded over HTTPS but a course uses HTTP.
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);

        String version = "1";
        try {
            version = getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
        } catch (Exception ignored) {
            // A missing version name must not stop the WebView.
        }
        settings.setUserAgentString(settings.getUserAgentString() + " OfflineUAndroid/" + version);

        CookieManager.getInstance().setAcceptCookie(true);

        webView.setBackgroundColor(0xFF070B16);
        webView.setOverScrollMode(View.OVER_SCROLL_NEVER);
        webView.setWebViewClient(new OfflineUClient());
        webView.setWebChromeClient(new OfflineUChromeClient());
        // There is no native download UI; hand downloads to the system browser.
        webView.setDownloadListener((url, userAgent, contentDisposition, mimeType, contentLength) ->
                openExternal(Uri.parse(url)));

        if ((getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0) {
            WebView.setWebContentsDebuggingEnabled(true);
        }
    }

    private void loadServer() {
        hideError();
        webView.loadUrl(serverUrl + "/");
    }

    private String currentUrl() {
        String url = webView.getUrl();
        return TextUtils.isEmpty(url) ? serverUrl + "/" : url;
    }

    private void updateToolbarSubtitle() {
        toolbar.setSubtitle(ServerConfig.displayAddress(serverUrl));
    }

    private void retry() {
        hideError();
        String target = webView.getUrl();
        if (TextUtils.isEmpty(target) || target.startsWith("about:")) {
            target = serverUrl + "/";
        }
        webView.loadUrl(target);
    }

    private void showError(String message) {
        errorMessage.setText(message);
        errorPanel.setVisibility(View.VISIBLE);
    }

    private void hideError() {
        errorPanel.setVisibility(View.GONE);
    }

    private void changeServer() {
        Intent intent = new Intent(this, LoginActivity.class);
        intent.putExtra(LoginActivity.EXTRA_CHANGE_SERVER, true);
        intent.putExtra(LoginActivity.EXTRA_PREFILL, serverUrl);
        startActivityForResult(intent, REQUEST_CHANGE_SERVER);
    }

    private void confirmClearSiteData() {
        new AlertDialog.Builder(this)
                .setTitle(R.string.clear_data_title)
                .setMessage(R.string.clear_data_message)
                .setPositiveButton(R.string.clear_data_confirm, (dialog, which) -> {
                    webView.clearCache(true);
                    webView.clearHistory();
                    CookieManager cookies = CookieManager.getInstance();
                    cookies.removeAllCookies(value -> cookies.flush());
                    Toast.makeText(this, R.string.clear_data_done, Toast.LENGTH_SHORT).show();
                    webView.reload();
                })
                .setNegativeButton(R.string.cancel, null)
                .show();
    }

    /** Navigation that leaves the OfflineU server goes to the system browser. */
    private boolean handleUri(Uri uri) {
        if (uri == null) {
            return false;
        }
        String scheme = uri.getScheme();
        if (scheme == null) {
            return false;
        }
        scheme = scheme.toLowerCase(Locale.US);

        if (scheme.equals("http") || scheme.equals("https")) {
            if (isSameServer(uri)) {
                return false;
            }
            openExternal(uri);
            return true;
        }
        // WebView internal schemes must stay inside the WebView (blob: is used
        // by HTML5 media and MSE playback).
        if (scheme.equals("about") || scheme.equals("data") || scheme.equals("blob")
                || scheme.equals("javascript")) {
            return false;
        }
        if (scheme.equals("intent")) {
            try {
                startActivity(Intent.parseUri(uri.toString(), Intent.URI_INTENT_SCHEME));
            } catch (Exception exception) {
                toastNoApp();
            }
            return true;
        }
        openExternal(uri);
        return true;
    }

    private boolean isSameServer(Uri candidate) {
        Uri base = Uri.parse(serverUrl);
        String candidateHost = candidate.getHost();
        String baseHost = base.getHost();
        if (candidateHost == null || baseHost == null) {
            return false;
        }
        if (!candidateHost.equalsIgnoreCase(baseHost)) {
            return false;
        }
        return effectivePort(candidate) == effectivePort(base);
    }

    private static int effectivePort(Uri uri) {
        int port = uri.getPort();
        if (port != -1) {
            return port;
        }
        return "https".equalsIgnoreCase(uri.getScheme()) ? 443 : 80;
    }

    private void openExternal(Uri uri) {
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, uri));
            Toast.makeText(this, R.string.external_opened, Toast.LENGTH_SHORT).show();
        } catch (ActivityNotFoundException exception) {
            toastNoApp();
        } catch (Exception exception) {
            toastNoApp();
        }
    }

    private void toastNoApp() {
        Toast.makeText(this, R.string.no_app_for_link, Toast.LENGTH_SHORT).show();
    }

    private void hideCustomView() {
        if (customView == null) {
            return;
        }
        fullscreenContainer.removeView(customView);
        fullscreenContainer.setVisibility(View.GONE);
        customView = null;
        setFullscreenUi(false);
        getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);

        WebChromeClient.CustomViewCallback callback = customViewCallback;
        customViewCallback = null;
        if (callback != null) {
            callback.onCustomViewHidden();
        }
    }

    @SuppressWarnings("deprecation")
    private void setFullscreenUi(boolean immersive) {
        View decor = getWindow().getDecorView();
        if (immersive) {
            decor.setSystemUiVisibility(
                    View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                            | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                            | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                            | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                            | View.SYSTEM_UI_FLAG_FULLSCREEN
                            | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY);
        } else {
            decor.setSystemUiVisibility(View.SYSTEM_UI_FLAG_VISIBLE);
        }
    }

    @SuppressWarnings("deprecation")
    @Override
    public void onBackPressed() {
        if (customView != null) {
            hideCustomView();
            return;
        }
        if (webView != null && webView.canGoBack()) {
            webView.goBack();
            return;
        }
        long now = System.currentTimeMillis();
        if (now - lastBackPress < 2000) {
            super.onBackPressed();
            return;
        }
        lastBackPress = now;
        Toast.makeText(this, R.string.back_again_to_exit, Toast.LENGTH_SHORT).show();
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);

        if (requestCode == REQUEST_FILE_CHOOSER) {
            if (filePathCallback == null) {
                return;
            }
            Uri[] result = null;
            if (resultCode == RESULT_OK && data != null) {
                result = WebChromeClient.FileChooserParams.parseResult(resultCode, data);
            }
            filePathCallback.onReceiveValue(result);
            filePathCallback = null;
            return;
        }

        if (requestCode == REQUEST_CHANGE_SERVER && resultCode == RESULT_OK && data != null) {
            String url = data.getStringExtra(LoginActivity.EXTRA_SERVER_URL);
            if (!TextUtils.isEmpty(url) && ServerConfig.isValid(url)) {
                serverUrl = url;
                updateToolbarSubtitle();
                loadServer();
            }
        }
    }

    @Override
    protected void onSaveInstanceState(Bundle outState) {
        if (webView != null) {
            webView.saveState(outState);
        }
        outState.putString(STATE_URL, serverUrl);
        outState.putBoolean(STATE_ERROR_VISIBLE,
                errorPanel != null && errorPanel.getVisibility() == View.VISIBLE);
        outState.putString(STATE_ERROR_TEXT,
                errorMessage != null ? errorMessage.getText().toString() : "");
        super.onSaveInstanceState(outState);
    }

    @Override
    protected void onPause() {
        if (webView != null) {
            webView.onPause();
        }
        CookieManager.getInstance().flush();
        super.onPause();
    }

    @Override
    protected void onResume() {
        super.onResume();
        if (webView != null) {
            webView.onResume();
        }
    }

    @Override
    protected void onDestroy() {
        if (webView != null) {
            webView.stopLoading();
            webView.destroy();
        }
        super.onDestroy();
    }

    private final class OfflineUClient extends WebViewClient {

        @Override
        public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
            return handleUri(request.getUrl());
        }

        @SuppressWarnings("deprecation")
        @Override
        public boolean shouldOverrideUrlLoading(WebView view, String url) {
            return handleUri(Uri.parse(url));
        }

        @Override
        public void onPageStarted(WebView view, String url, Bitmap favicon) {
            progress.setProgress(0);
            progress.setVisibility(View.VISIBLE);
            hideError();
        }

        @Override
        public void onPageFinished(WebView view, String url) {
            progress.setVisibility(View.GONE);
        }

        @Override
        public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
            if (!request.isForMainFrame()) {
                return;
            }
            progress.setVisibility(View.GONE);
            String detail = error.getDescription() == null ? "" : error.getDescription().toString();
            String message = getString(R.string.main_error_message, serverUrl);
            if (!detail.isEmpty()) {
                message = message + "\n\n" + detail;
            }
            showError(message);
        }

        @Override
        public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
            new AlertDialog.Builder(MainActivity.this)
                    .setTitle(R.string.ssl_title)
                    .setMessage(getString(R.string.ssl_message, error.getUrl()))
                    .setPositiveButton(R.string.ssl_continue, (dialog, which) -> handler.proceed())
                    .setNegativeButton(R.string.cancel, (dialog, which) -> handler.cancel())
                    .setOnCancelListener(dialog -> handler.cancel())
                    .show();
        }
    }

    private final class OfflineUChromeClient extends WebChromeClient {

        @Override
        public void onProgressChanged(WebView view, int newProgress) {
            progress.setProgress(newProgress);
            if (newProgress >= 100) {
                progress.setVisibility(View.GONE);
            }
        }

        @Override
        public void onShowCustomView(View view, CustomViewCallback callback) {
            if (customView != null) {
                callback.onCustomViewHidden();
                return;
            }
            customView = view;
            customViewCallback = callback;
            fullscreenContainer.addView(view, new FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT));
            fullscreenContainer.setVisibility(View.VISIBLE);
            setFullscreenUi(true);
            getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        }

        @Override
        public void onHideCustomView() {
            hideCustomView();
        }

        @Override
        public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> callback,
                                         FileChooserParams params) {
            if (filePathCallback != null) {
                filePathCallback.onReceiveValue(null);
            }
            filePathCallback = callback;
            try {
                Intent intent = params.createIntent();
                startActivityForResult(
                        Intent.createChooser(intent, getString(R.string.file_chooser_title)),
                        REQUEST_FILE_CHOOSER);
                return true;
            } catch (Exception exception) {
                filePathCallback = null;
                callback.onReceiveValue(null);
                toastNoApp();
                return false;
            }
        }
    }
}
