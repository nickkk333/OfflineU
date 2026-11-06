# OfflineU for Android

这是一个轻量的 **WebView 外壳**，不是第二套原生界面。它加载的页面、调用的 API、
课程列表与学习进度，和你用桌面浏览器打开的是同一套 OfflineU。因此 Android 端不需要
同步任何数据：进度、排除名单、WebDAV/SMB 课程源都由服务器保存。

主要功能：

* 首次启动填写服务器地址，自动规范化 `192.168.1.10:5000` → `http://192.168.1.10:5000`。
* 连接前请求 `/health`；服务器正在启动或健康检查被拦截时，可以选择“仍然打开”。
* 地址保存在本机 `SharedPreferences`，下次启动直接进入。
* 完整的 Vue 页面：课程选择器、课程源管理、视频/音频播放器、文档预览、投屏、进度。
* 视频全屏、返回键（网页前进/后退 + 再按一次退出）、外链交给系统浏览器。
* 自签名 HTTPS 证书会弹出确认；支持网页里的文件选择器。
* 工具栏菜单：刷新、在浏览器中打开、更换服务器、清除站点数据。

## 环境要求

| 项目 | 版本 |
| ---- | ---- |
| JDK | 17 |
| Android Gradle Plugin | 8.5.2 |
| Gradle | 8.7（已附带 wrapper） |
| Android SDK | Platform 34 / Build-Tools 34.x |
| minSdk | 24（Android 7.0） |
| targetSdk | 34（Android 14） |
| 依赖 | 无 AndroidX、无第三方库 |

项目只使用 `android.*` 平台 API，所以不需要 AndroidX、Kotlin 或网络库。

## 构建

### Android Studio（推荐）

1. 安装 Android Studio，并在 SDK Manager 里安装 **Android 14 (API 34)** SDK 与 Build-Tools。
2. `File → Open`，选择本目录 `android/`（不是仓库根目录）。
3. 等待 Gradle Sync 完成。
4. `Run → Run 'app'` 直接安装到手机，或 `Build → Build Bundle(s) / APK(s) → Build APK(s)`。

### 命令行

Windows：

```bat
cd android
gradlew.bat assembleDebug
```

macOS / Linux：

```bash
cd android
chmod +x gradlew
./gradlew assembleDebug
```

产物：

```text
android/app/build/outputs/apk/debug/app-debug.apk
```

GitHub Actions 每次推送都会构建这个 debug APK，命名为
`offlineu-android-<版本>.apk`（日常构建版本为 `latest`），上传为 CI artifact；
打 `v*` tag 时会自动挂到对应的 GitHub Release。tag 构建会用
`-PappVersionName=<版本>` 和 `-PappVersionCode=主版本*10000+次版本*100+修订号`
注入版本号，本地构建则沿用 `app/build.gradle` 里的默认值。

安装：

```bash
adb install -r android/app/build/outputs/apk/debug/app-debug.apk
```

如果没用 wrapper，也可以用本机的 Gradle 8.7：

```bash
cd android
gradle assembleDebug
```

如果 SDK 不在默认位置，设置 `ANDROID_HOME` / `ANDROID_SDK_ROOT`，或在
`android/local.properties` 里写：

```properties
sdk.dir=C\:\\Users\\you\\AppData\\Local\\Android\\Sdk
```

`local.properties` 不应提交到仓库（`.gitignore` 已包含）。

## 首次连接

1. 确认 OfflineU 服务器监听局域网地址。Docker 镜像默认就是 `OFFLINEU_HOST=0.0.0.0`；
   本地 `go run .` 需要显式加 `--host 0.0.0.0`（或设置 `OFFLINEU_HOST=0.0.0.0`）。
2. 手机与电脑/NAS 连同一个 Wi-Fi。
3. 在登录页填写 `http://<电脑或 NAS 的局域网 IP>:5000`，例如
   `http://192.168.1.10:5000`。
4. （可选）在「课程来源（可选）」里选择 **WebDAV / SMB / 服务器本地目录**，填写地址、
   共享名或目录和凭据。连接时调用 `/api/sources/connect` 先验证再保存，凭据只存在服务器；
   选择“使用服务器上已配置的来源”即跳过这一步。
5. 点“连接”。如果 `/health` 返回 `{"status":"healthy"}`（且来源验证通过）就直接进入；
   否则会说明原因，并允许“仍然打开”或“不添加来源，直接打开”。

不要在手机上填写 `127.0.0.1` 或 `localhost` —— 那指向手机自己。

### 更换服务器

进入网页后，点右上角工具栏的 **⋮ → 更换服务器**。输入新地址并连接，当前 WebView
会切换到新服务器；旧地址仍保留在输入框里，方便切回。

### 清除站点数据

**⋮ → 清除站点数据** 会清除 WebView 缓存、Cookie 和浏览历史。服务器上的课程文件、
课程源和学习进度不受影响。

## 与 WebDAV / SMB / 本地课程源配合

登录页自带「课程来源（可选）」表单，选中类型后填写对应字段；进入网页后也可以打开
`/login` 连接页或课程选择器里的 **课程来源** 面板（支持编辑 ✎、测试、删除）：

* **本地文件夹** —— 服务器上的目录（受 `OFFLINEU_ROOTS` 白名单限制）。
* **WebDAV** —— 地址、用户名、密码；网页里可接受自签名证书。
* **SMB / Windows 共享** —— 主机、共享名、共享内路径、用户名、密码；网页里还可填端口与域。

登录页提交时调用 `POST /api/sources/connect`：服务端先实测连接、再写入 `sources.json`
（权限 `0600`），同一地址重复登录会复用已有来源。密码**只保存在服务器**，不会写进手机的
SharedPreferences，接口也只返回 `has_password`。连接失败时可以“不添加来源，直接打开”，
之后在网页里补配；从菜单「更换服务器」进入时不会向旧服务器添加来源。
删除课程源不会删除课程文件、进度或最近课程。

远程课程源按原始字节流播放，服务器不会为它们做 HLS/重新封装，因此能否播放取决于
WebView 的编解码支持（例如 H.264/AAC 的 MP4 通常没问题；部分 MKV/AVI 可能无法播放）。
学习进度仍然由服务器记录在 `OFFLINEU_PROGRESS_DIR`（或默认 `data/`）下，不会写入
远程目录。

## 安全说明

* 允许明文 HTTP 是为了让手机直接访问局域网里的 `http://<ip>:5000`。
  把 OfflineU 暴露到公网时请使用 HTTPS 反向代理。
* 应用在 `res/xml/network_security_config.xml` 中同时信任系统与用户安装的 CA。
  使用自签名证书的 NAS 可以安装该 CA；否则每次启动会弹出证书确认。
* 应用只申请 `INTERNET`，不申请存储权限；网页文件选择器走系统文件选择器。
* 地址只保存在本机，不参与任何云同步；课程来源的密码不保存在手机，只保存到服务器。

## 项目结构

```text
android/
├── settings.gradle
├── build.gradle                 # AGP 8.5.2
├── gradle.properties
├── gradlew / gradlew.bat        # Gradle 8.7 wrapper
├── gradle/wrapper/              # wrapper jar + properties
└── app/
    ├── build.gradle
    └── src/main/
        ├── AndroidManifest.xml
        ├── java/com/nickkk333/offlineu/
        │   ├── ServerConfig.java   # 地址规范化 + SharedPreferences
        │   ├── LoginActivity.java  # 服务器地址、课程来源表单、/health、/api/sources/connect
        │   └── MainActivity.java   # WebView、全屏、菜单、返回键、SSL
        └── res/
            ├── layout/             # 登录页 + WebView 外壳
            ├── drawable/           # 图标 / 背景
            ├── mipmap-*/           # 启动图标
            ├── values/             # 英文文案、颜色、主题、来源类型、尺寸
            ├── values-zh/          # 中文文案
            ├── values-land/        # 横屏内边距
            ├── values-sw600dp/     # 平板内边距
            ├── menu/               # 工具栏菜单
            └── xml/                # 网络安全配置
```

`MainActivity` 在 `onSaveInstanceState` 里保存 WebView 状态，旋转屏幕不会重新加载课程；
全屏视频使用 `WebChromeClient.onShowCustomView`，返回键先退出全屏，再执行网页后退，
最后才是“再按一次退出”。

## 发布签名

调试包可以直接侧载。发布到应用商店或长期分发时，用 Android Studio 的
`Build → Generate Signed App Bundle / APK` 创建 keystore 并选择 `release` 构建；
也可以自行配置 `signingConfigs`，不要把 keystore 和密码提交到仓库。

发布前记得修改 `app/build.gradle` 里的 `versionCode` / `versionName`：

```gradle
defaultConfig {
    versionCode 2
    versionName "1.1.0"
}
```

## 调试

调试构建（`FLAG_DEBUGGABLE`）自动开启 WebView 远程调试：

1. 手机连接 USB，确认 `adb devices` 能看到设备。
2. 电脑 Chrome 打开 `chrome://inspect`。
3. 在列表里选择 OfflineU 的 WebView，即可使用完整的 DevTools（Console、Network、Elements）。

也可以用 `adb logcat` 查看 `chromium` 相关日志：

```bash
adb logcat | grep -i chromium
```

## 已知限制

* WebView 播放能力取决于系统 WebView 版本；远程课程源不转码时，冷门容器/编码可能无法播放。
* 投屏（DLNA）要求手机与投屏设备在同一局域网；服务器端的 `OFFLINEU_DLNA` 必须开启。
* 下载链接交给系统浏览器处理；需要登录的下载地址在 OfflineU 当前版本中不适用
  （OfflineU 本身没有账号体系）。
