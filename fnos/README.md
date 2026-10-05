# OfflineU 飞牛OS（fnOS）应用包

本目录把 OfflineU 打包成飞牛OS 应用中心可直接安装的 `.fpk` 包（仅 **amd64 / x86**）。
**完全离线**：Docker 镜像随包内置，安装阶段由 `cmd/install_init` 执行 `docker load`，
飞牛OS 全程不访问任何镜像仓库。

## 目录结构

```
fnos/
├── offlineu/                 # fnOS 应用工程（fnpack 期望的源树）
│   ├── manifest             # 应用元信息（appname/version/platform=x86/端口等）
│   ├── ICON.PNG             # 64×64 图标
│   ├── ICON_256.PNG         # 256×256 图标
│   ├── app/docker/
│   │   ├── docker-compose.yaml  # image: offlineu:local + pull_policy: never（bridge+端口）
│   │   └── offlineu-image.tar   # 本地构建出的镜像（打包时生成，*.tar 已被 gitignore）
│   ├── cmd/install_init     # 安装最早期加载内置镜像（早于容器启动）
│   ├── cmd/install_callback # 镜像加载兜底 + 校验
│   ├── cmd/main             # 生命周期 no-op（docker-project 管理容器）
│   ├── config/privilege     # 运行身份
│   ├── config/resource      # docker-project + 共享目录声明
│   └── wizard/install       # 安装向导（课程目录、端口、时区）
└── build.ps1                # 本地一键打包（Windows：构建镜像 + 导出 + fnpack）
```

## 为什么用 docker-project（而非 native）

飞牛OS 的 `docker-project` 资源会让应用中心负责拉起/暴露容器端口。若改成 native 应用
（脚本自己 `docker compose up`），应用中心不会为容器做端口暴露/反代，且会因主进程
（`cmd/main`）很快退出而把应用标记为"已停止"——表现为「启动后自动停止、无法访问」。
因此本包用 `docker-project` 让 fnOS 正常管理容器，并在**安装钩子**（`install_init`，
早于容器启动）里 `docker load` 内置镜像，从而既离线又能被正确托管。

## 打包

### 本地打包（推荐，完全离线）

`fnos/build.ps1` 在本机完成：① `docker build --platform linux/amd64 -t offlineu:local`
（含前端 + 内置 ffmpeg）→ ② `docker save` 导出到 `app/docker/offlineu-image.tar`
→ ③ 把包内文本统一成 LF → ④ 官方 `fnpack` 打包成 `offlineu_1.0.0_x86.fpk`。

```powershell
cd fnos
.\build.ps1            # 产物 fnos\offlineu_1.0.0_x86.fpk
```

要求：Windows 上已启动 Docker Desktop（Linux 容器模式），且首次运行能联网下载 fnpack
与拉取基础镜像（构建阶段联网，产物对 NAS 完全离线）。

### CI 打包（GitHub Actions）

`.github/workflows/docker-build.yml` 的 `fpk` job：用 `docker buildx` 构建 amd64 镜像并
`docker save` 进包树，再用官方 `fnpack` 打包，产物上传为 Artifacts（`offlineu-fpk-x86`）；
打 `v*` tag 时还会附到对应 Release。CI 同样内置镜像，不依赖 ghcr 拉取。

> 注：`build` job 仍会把镜像推到 `ghcr.io/nickkk333/offlineu:main` 作为在线备用，但本
> fpk 的 compose 已写死 `offlineu:local` + `pull_policy: never`，安装时不会去 ghcr。

## 安装（离线，无镜像仓库）

1. 飞牛OS → 应用中心 → 手动安装 → 上传 `offlineu_1.0.0_x86.fpk`。
2. 向导中填写：
   - **课程目录（绝对路径）**：宿主机上课程文件夹的绝对路径（如 `/vol1/1000/offlineu/courses`），
     容器内挂到 `/courses`（只读）。
   - **服务端口**（默认 5000）、**时区**。
3. 安装阶段 `cmd/install_init` 自动 `docker load` 包内镜像，随后 docker-project 以
   `offlineu:local` 拉起容器（bridge 网络 + 端口映射，应用中心据此暴露端口），**无需联网**。
4. 进度数据固定写在安装目录下的 `app/docker/data`（无需指定）。
5. 浏览器打开 `http://<NAS IP>:<端口>` 即可播放；MKV / 伪 .mp4(MPEG-TS) 等会被服务端实时
   remux 为可拖动的 MP4。

## 排错

- **应用包不符合系统要求**：fpk 不是 `fnpack` 生成，或 `manifest` 的 `platform` 不是
  `x86`/`arm`，或包内脚本是 CRLF（本仓库已用 `.gitattributes` + 构建脚本强制 LF）。
- **安装后容器起不来 / 端口不通**：查看 `app/docker/install.log`（安装钩子把镜像加载结果
  写在这里）。若提示镜像 tar 未找到或 load 失败，说明包内镜像缺失，请用本仓库脚本重新构建。
- 容器日志可在飞牛「Docker」应用里看 `offlineu` 容器。
