# OfflineU 飞牛OS（fnOS）应用包

本目录把 OfflineU 打包成飞牛OS 应用中心可直接安装的 `.fpk` 包（仅 **amd64 / x86**）。

## 目录结构

```
fnos/
├── offlineu/                 # fnOS 应用工程（fnpack 期望的源树）
│   ├── manifest             # 应用元信息（appname/version/platform=x86/端口等）
│   ├── ICON.PNG             # 64×64 图标
│   ├── ICON_256.PNG         # 256×256 图标
│   ├── app/docker/docker-compose.yaml  # 实际运行的容器（引用 ghcr 镜像）
│   ├── cmd/main             # 生命周期入口（start/stop/status）
│   ├── config/privilege     # 运行身份
│   ├── config/resource      # 共享目录声明
│   └── wizard/install       # 安装向导（端口、时区）
└── build.ps1                # 本地打包（Windows，自动下载官方 fnpack）
```

## 打包（推荐：GitHub Actions）

`.github/workflows/docker-build.yml` 在推送时自动完成：

1. 构建并推送镜像 `ghcr.io/nickkk333/offlineu:main`（仅 `linux/amd64`，内置 ffmpeg）；
2. 用官方 `fnpack` 把 `offlineu/` 打成 `offlineu_1.0.0_x86.fpk`；
3. 把 fpk 上传为构建产物（Actions 运行页 → Artifacts，名称 `offlineu-fpk-x86`）；
   打 `v*` tag 时还会自动附加到对应 GitHub Release。

在 Actions 运行页下载该产物即可获得可安装的 fpk。

> **为什么必须用 fnpack**：fpk 的 `app/` 在包内是 `app.tgz` 等特定结构，手工 `tar.gz`
> 的平铺目录不被 fnOS 识别，安装会报「应用包不符合系统要求」。
> 另外 `manifest` 的 `platform` 必须是 `x86`（或 `arm`），**不能写 `all`**。

## 本地打包（可选）

Windows 下脚本会自动下载官方 `fnpack`（来自 `static2.fnnas.com`）并打包：

```powershell
cd fnos
.\build.ps1            # 产物 fnos\offlineu_1.0.0_x86.fpk
```

## 安装

1. 飞牛OS → 应用中心 → 手动安装 → 上传 `offlineu_1.0.0_x86.fpk`。
2. 向导中设置对外端口（默认 5000）与时区。
3. 首次启动会拉取镜像 `ghcr.io/nickkk333/offlineu:main`（amd64）；
   若拉取缓慢/失败，请为 Docker 配置国内镜像加速，或在能联网的机器上拉取后导入。
4. 把课程文件夹放入应用对应的 `courses` 共享目录（只读挂载到容器内 `/courses`），
   浏览器打开 `http://<NAS IP>:<端口>` 即可播放；MKV / 伪 .mp4(MPEG-TS) 等会被服务端实时
   remux 为可拖动的 MP4。

## 常见报错

- **应用包不符合系统要求**：fpk 不是 `fnpack` 生成的，或 `manifest` 的 `platform` 不是
  `x86`/`arm`。
- **安装后容器起不来**：多为 ghcr 拉取失败，检查网络或改用镜像加速。
