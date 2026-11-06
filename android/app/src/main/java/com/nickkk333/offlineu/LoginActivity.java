package com.nickkk333.offlineu;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.TextUtils;
import android.view.View;
import android.view.ViewGroup;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputMethodManager;
import android.widget.AdapterView;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.ProgressBar;
import android.widget.Spinner;
import android.widget.TextView;

import org.json.JSONException;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * First screen: ask for the OfflineU server URL and, optionally, sign in with a
 * WebDAV / SMB / local course source. The health check is deliberately soft -
 * a server that is still starting can be opened anyway. Source credentials are
 * posted to the server (never stored on the phone) through
 * /api/sources/connect, which verifies them before saving anything.
 */
public class LoginActivity extends Activity {

    /** Set when MainActivity asks for a new address instead of app startup. */
    static final String EXTRA_CHANGE_SERVER = "change_server";
    /** The normalized address returned to MainActivity. */
    static final String EXTRA_SERVER_URL = "server_url";
    /** Optional address to pre-fill when changing servers. */
    static final String EXTRA_PREFILL = "prefill_url";

    private static final int KIND_NONE = 0;
    private static final int KIND_WEBDAV = 1;
    private static final int KIND_SMB = 2;
    private static final int KIND_LOCAL = 3;
    /** buildSourceJson() returns this when a selected source is incomplete. */
    private static final String SOURCE_INCOMPLETE = "";

    private EditText serverInput;
    private Button connectButton;
    private ProgressBar loginProgress;
    private TextView loginStatus;

    private View sourceSection;
    private Spinner sourceKind;
    private View webdavFields;
    private View smbFields;
    private View localFields;
    private EditText sourceUrl;
    private EditText sourceWebdavUser;
    private EditText sourceWebdavPassword;
    private EditText sourceHost;
    private EditText sourceShare;
    private EditText sourceBasePath;
    private EditText sourceSmbUser;
    private EditText sourceSmbPassword;
    private EditText sourcePath;

    private final ExecutorService executor = Executors.newSingleThreadExecutor();
    private final Handler mainHandler = new Handler(Looper.getMainLooper());
    private boolean checking;
    private boolean changingServer;
    private boolean pendingSource;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_login);

        serverInput = findViewById(R.id.server_url);
        connectButton = findViewById(R.id.connect_button);
        loginProgress = findViewById(R.id.login_progress);
        loginStatus = findViewById(R.id.login_status);

        bindSourceFields();

        changingServer = getIntent().getBooleanExtra(EXTRA_CHANGE_SERVER, false);
        if (changingServer) {
            // Changing the address must not add a source to the old server.
            sourceSection.setVisibility(View.GONE);
        } else {
            setupSourceSpinner();
        }

        String prefill = getIntent().getStringExtra(EXTRA_PREFILL);
        if (TextUtils.isEmpty(prefill)) {
            prefill = ServerConfig.load(this);
        }
        if (!TextUtils.isEmpty(prefill)) {
            serverInput.setText(prefill);
            serverInput.setSelection(prefill.length());
        }

        connectButton.setOnClickListener(view -> connect());
        serverInput.setOnEditorActionListener((view, actionId, event) -> {
            if (actionId == EditorInfo.IME_ACTION_GO || actionId == EditorInfo.IME_ACTION_DONE) {
                connect();
                return true;
            }
            return false;
        });
    }

    private void bindSourceFields() {
        sourceSection = findViewById(R.id.source_section);
        sourceKind = findViewById(R.id.source_kind);
        webdavFields = findViewById(R.id.source_webdav);
        smbFields = findViewById(R.id.source_smb);
        localFields = findViewById(R.id.source_local);
        sourceUrl = findViewById(R.id.source_url);
        sourceWebdavUser = findViewById(R.id.source_webdav_user);
        sourceWebdavPassword = findViewById(R.id.source_webdav_password);
        sourceHost = findViewById(R.id.source_host);
        sourceShare = findViewById(R.id.source_share);
        sourceBasePath = findViewById(R.id.source_base_path);
        sourceSmbUser = findViewById(R.id.source_smb_user);
        sourceSmbPassword = findViewById(R.id.source_smb_password);
        sourcePath = findViewById(R.id.source_path);
    }

    private void setupSourceSpinner() {
        String[] kinds = getResources().getStringArray(R.array.login_source_kinds);
        ArrayAdapter<String> adapter = new ArrayAdapter<String>(this,
                android.R.layout.simple_spinner_item, kinds) {
            @Override
            public View getView(int position, View convertView, ViewGroup parent) {
                TextView label = (TextView) super.getView(position, convertView, parent);
                label.setTextColor(getColor(R.color.text));
                label.setTextSize(15);
                return label;
            }

            @Override
            public View getDropDownView(int position, View convertView, ViewGroup parent) {
                TextView label = (TextView) super.getDropDownView(position, convertView, parent);
                label.setTextColor(getColor(R.color.text));
                return label;
            }
        };
        adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);
        sourceKind.setAdapter(adapter);
        sourceKind.setPopupBackgroundDrawable(getDrawable(R.drawable.bg_card));
        sourceKind.setOnItemSelectedListener(new AdapterView.OnItemSelectedListener() {
            @Override
            public void onItemSelected(AdapterView<?> parent, View view, int position, long id) {
                showSourceFields(position);
            }

            @Override
            public void onNothingSelected(AdapterView<?> parent) {
                showSourceFields(KIND_NONE);
            }
        });
        showSourceFields(KIND_NONE);
    }

    private void showSourceFields(int kind) {
        webdavFields.setVisibility(kind == KIND_WEBDAV ? View.VISIBLE : View.GONE);
        smbFields.setVisibility(kind == KIND_SMB ? View.VISIBLE : View.GONE);
        localFields.setVisibility(kind == KIND_LOCAL ? View.VISIBLE : View.GONE);
    }

    private void connect() {
        if (checking) {
            return;
        }

        String url = ServerConfig.normalize(serverInput.getText().toString());
        if (!ServerConfig.isValid(url)) {
            showStatus(getString(R.string.login_invalid_url), true);
            serverInput.requestFocus();
            return;
        }

        // null = no source selected, SOURCE_INCOMPLETE = missing fields.
        final String sourceJson = changingServer ? null : buildSourceJson();
        if (SOURCE_INCOMPLETE.equals(sourceJson)) {
            showStatus(getString(R.string.login_source_incomplete), true);
            return;
        }

        serverInput.setText(url);
        serverInput.setSelection(url.length());
        hideKeyboard();
        pendingSource = sourceJson != null;
        setChecking(true);

        executor.execute(() -> {
            String healthError = probe(url);
            String sourceError = null;
            if (healthError == null && sourceJson != null) {
                sourceError = connectSource(url, sourceJson);
            }
            final String error = sourceError != null ? sourceError : healthError;
            final boolean sourceFailed = sourceError != null;
            mainHandler.post(() -> {
                if (isFinishing() || isDestroyed()) {
                    return;
                }
                setChecking(false);
                if (error == null) {
                    finishWithUrl(url);
                } else if (sourceFailed) {
                    showStatus(error, true);
                    showSkipSource(url, error);
                } else {
                    showStatus(error, true);
                    showOpenAnyway(url, error);
                }
            });
        });
    }

    /** JSON body for /api/sources/connect, or null / SOURCE_INCOMPLETE. */
    private String buildSourceJson() {
        int kind = sourceKind.getSelectedItemPosition();
        if (kind == KIND_NONE) {
            return null;
        }
        try {
            JSONObject source = new JSONObject();
            if (kind == KIND_WEBDAV) {
                String target = text(sourceUrl);
                if (TextUtils.isEmpty(target)) {
                    return SOURCE_INCOMPLETE;
                }
                source.put("kind", "webdav");
                source.put("url", target);
                source.put("username", text(sourceWebdavUser));
                source.put("password", raw(sourceWebdavPassword));
            } else if (kind == KIND_SMB) {
                String host = text(sourceHost);
                String share = text(sourceShare);
                if (TextUtils.isEmpty(host) || TextUtils.isEmpty(share)) {
                    return SOURCE_INCOMPLETE;
                }
                source.put("kind", "smb");
                source.put("host", host);
                source.put("share", share);
                source.put("base_path", text(sourceBasePath));
                source.put("username", text(sourceSmbUser));
                source.put("password", raw(sourceSmbPassword));
            } else if (kind == KIND_LOCAL) {
                String path = text(sourcePath);
                if (TextUtils.isEmpty(path)) {
                    return SOURCE_INCOMPLETE;
                }
                source.put("kind", "local");
                source.put("path", path);
            } else {
                return null;
            }
            // The phone opens the course picker; the user chooses the course
            // folder there instead of loading the source root blindly.
            source.put("load", false);
            return source.toString();
        } catch (JSONException exception) {
            return SOURCE_INCOMPLETE;
        }
    }

    private static String text(EditText field) {
        return field.getText().toString().trim();
    }

    private static String raw(EditText field) {
        return field.getText().toString();
    }

    /** Returns null when the server accepted the source, otherwise an error. */
    private String connectSource(String baseUrl, String json) {
        HttpURLConnection connection = null;
        try {
            URL endpoint = new URL(baseUrl + "/api/sources/connect");
            connection = (HttpURLConnection) endpoint.openConnection();
            connection.setRequestMethod("POST");
            connection.setConnectTimeout(10000);
            // SMB dialing and WebDAV PROPFIND can take a while on a NAS.
            connection.setReadTimeout(90000);
            connection.setDoOutput(true);
            connection.setUseCaches(false);
            connection.setInstanceFollowRedirects(true);
            connection.setRequestProperty("Content-Type", "application/json; charset=utf-8");
            connection.setRequestProperty("Accept", "application/json");
            connection.setRequestProperty("Accept-Language", Locale.getDefault().toLanguageTag());
            try (OutputStream output = connection.getOutputStream()) {
                output.write(json.getBytes(StandardCharsets.UTF_8));
            }
            int code = connection.getResponseCode();
            if (code == HttpURLConnection.HTTP_OK) {
                return null;
            }
            String body = "";
            try (InputStream input = connection.getErrorStream()) {
                if (input != null) {
                    body = readBody(input);
                }
            }
            String message = errorMessage(body);
            return TextUtils.isEmpty(message) ? getString(R.string.login_http_error, code) : message;
        } catch (Exception exception) {
            String message = exception.getMessage();
            return TextUtils.isEmpty(message) ? exception.getClass().getSimpleName() : message;
        } finally {
            if (connection != null) {
                connection.disconnect();
            }
        }
    }

    private static String errorMessage(String body) {
        if (TextUtils.isEmpty(body)) {
            return "";
        }
        try {
            return new JSONObject(body).optString("error", "");
        } catch (JSONException exception) {
            return body.length() > 200 ? body.substring(0, 200) : body;
        }
    }

    /**
     * Returns null when the address looks like an OfflineU server, otherwise a
     * message explaining what went wrong.
     */
    private String probe(String baseUrl) {
        HttpURLConnection connection = null;
        try {
            URL healthUrl = new URL(baseUrl + "/health");
            connection = (HttpURLConnection) healthUrl.openConnection();
            connection.setConnectTimeout(8000);
            connection.setReadTimeout(8000);
            connection.setInstanceFollowRedirects(true);
            connection.setUseCaches(false);
            connection.setRequestProperty("Accept", "application/json");

            int code = connection.getResponseCode();
            if (code != HttpURLConnection.HTTP_OK) {
                return getString(R.string.login_http_error, code);
            }

            String body;
            try (InputStream input = connection.getInputStream()) {
                body = readBody(input);
            }
            if (body.toLowerCase(Locale.US).contains("healthy")) {
                return null;
            }
            return getString(R.string.login_not_offlineu);
        } catch (Exception exception) {
            String message = exception.getMessage();
            if (TextUtils.isEmpty(message)) {
                message = exception.getClass().getSimpleName();
            }
            return message;
        } finally {
            if (connection != null) {
                connection.disconnect();
            }
        }
    }

    private static String readBody(InputStream input) throws IOException {
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        byte[] buffer = new byte[4096];
        int read;
        while ((read = input.read(buffer)) != -1 && output.size() < 16384) {
            output.write(buffer, 0, read);
        }
        return new String(output.toByteArray(), StandardCharsets.UTF_8);
    }

    private void setChecking(boolean value) {
        checking = value;
        connectButton.setEnabled(!value);
        serverInput.setEnabled(!value);
        sourceKind.setEnabled(!value);
        loginProgress.setVisibility(value ? View.VISIBLE : View.GONE);
        if (value) {
            showStatus(getString(pendingSource
                    ? R.string.login_source_connecting
                    : R.string.login_checking), false);
        } else {
            loginStatus.setVisibility(View.GONE);
        }
    }

    private void showStatus(String message, boolean isError) {
        loginStatus.setText(message);
        loginStatus.setTextColor(getColor(isError ? R.color.danger : R.color.text_muted));
        loginStatus.setVisibility(TextUtils.isEmpty(message) ? View.GONE : View.VISIBLE);
    }

    private void showOpenAnyway(String url, String error) {
        new AlertDialog.Builder(this)
                .setTitle(R.string.login_save_anyway_title)
                .setMessage(getString(R.string.login_save_anyway_message, url, error))
                .setPositiveButton(R.string.login_open_anyway, (dialog, which) -> finishWithUrl(url))
                .setNegativeButton(R.string.cancel, null)
                .show();
    }

    private void showSkipSource(String url, String error) {
        new AlertDialog.Builder(this)
                .setTitle(R.string.login_source_failed_title)
                .setMessage(getString(R.string.login_source_failed_message, error))
                .setPositiveButton(R.string.login_open_without_source, (dialog, which) -> finishWithUrl(url))
                .setNegativeButton(R.string.cancel, null)
                .show();
    }

    private void finishWithUrl(String url) {
        ServerConfig.save(this, url);

        Intent result = new Intent();
        result.putExtra(EXTRA_SERVER_URL, url);
        setResult(RESULT_OK, result);

        if (getIntent().getBooleanExtra(EXTRA_CHANGE_SERVER, false)) {
            finish();
            return;
        }

        startActivity(new Intent(this, MainActivity.class)
                .putExtra(MainActivity.EXTRA_SERVER_URL, url));
        finish();
    }

    private void hideKeyboard() {
        View focus = getCurrentFocus();
        if (focus == null) {
            return;
        }
        InputMethodManager manager = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
        if (manager != null) {
            manager.hideSoftInputFromWindow(focus.getWindowToken(), 0);
        }
        focus.clearFocus();
    }

    @Override
    protected void onDestroy() {
        executor.shutdownNow();
        super.onDestroy();
    }
}
