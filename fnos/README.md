# OfflineU 飞牛OS（fnOS）应用包

本目录把 OfflineU 打包成飞牛OS 应用中心可直接安装的 `.fpk` 包。

## 目录结构

```
fnos/
├── offlineu/                 # fnOS 应用工程（fnpack 期望的源树）
│   ├── manifest             # 应用元信息（appname/version/端口等）
│   ├── ICON.PNG             # 64×64 图标
│   ├── ICON_256.PNG         # 256×256 图标
│   ├── app/docker/docker-compose.yaml  # 实际运行的容器（引用多架构镜像）
│   ├── cmd/main             # 生命周期入口（start/stop/status）
│   ├── config/privilege     # 运行身份
│   ├── config/resource      # 共享目录声明
│   └── wizard/install       # 安装向导（端口、时区）
├── offlineu-1.0.0.fpk       # 已构建的安装包
└── build.ps1                # 重建脚本
```

## 构建

优先使用飞牛官方 `fnpack`（文档：https://developer.fnnas.com/docs/cli/fnpack/）：

```powershell
cd fnos\offlineu
fnpack build          # 产物 offlineu-1.0.0.fpk
```

若未安装 `fnpack`，可运行提供的脚本（按官方文档的 `manifest + 应用树` 布局打成 tar.gz）：

```powershell
cd fnos
.\build.ps1
```

## 镜像（关键）

`app/docker/docker-compose.yaml` 引用 `ghcr.io/nickkk333/offlineu:main`，该镜像由
`.github/workflows/docker-build.yml` 在推送时**自动构建为多架构（linux/amd64 + linux/arm64）**，
因此可在 x86 与 ARM 的飞牛NAS 上运行。安装前请确认 CI 已成功构建出多架构镜像
（首次安装时 `pull_policy: always` 会自动拉取）。

## 安装

1. 飞牛OS → 应用中心 → 手动安装 → 上传 `offlineu-1.0.0.fpk`。
2. 向导中设置对外端口（默认 5000）与时区。
3. 安装完成后，把课程文件夹放入应用对应的 `courses` 共享目录（只读挂载到容器内 `/courses`），
   浏览器打开 `http://<NAS IP>:<端口>` 即可播放；MKV / 伪 .mp4(MPEG-TS) 等会被服务端实时
   remux 为可拖动的 MP4。
