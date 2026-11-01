# 本地 k3d 演练

`deployments/k3d.py` 在一台主机上创建独立的 k3d 集群和 kubeconfig，不切换当前默认 Kubernetes context。私有凭据和验收产物保存在仓外。

```bash
python3 deployments/k3d.py up
python3 deployments/k3d.py deploy
python3 deployments/k3d.py status
python3 deployments/k3d.py smoke
```

脚本支持 `up`、`deploy`、`status`、`smoke` 四个动作。`smoke` 覆盖 Service DNS/HTTP、数据库 DNS、Pod 重建、Worker 1→2→1 和滚动重启。默认集群使用 fixture 模型，不访问外部付费模型。默认前端转发端口为 18080（Client）、18081（Admin）、18082（Portal）；`status` 输出实际转发命令。

需要与其他本地集群或验收任务共存时，使用独立集群名、镜像后缀、端口和仓外输出目录：

```bash
export CAW_K3D_CLUSTER=caw-i17-k3d-20261031
export CAW_K3D_IMAGE_SUFFIX=i17-20261031
export CAW_K3D_API_PORT=28170
export CAW_K3D_CLIENT_PORT=28171 CAW_K3D_ADMIN_PORT=28172 CAW_K3D_PORTAL_PORT=28173
export CAW_K3D_ARTIFACT_DIR=/abs/path/outside/repo
```

`deployments/scripts/package-app-images.py --tag-suffix <后缀>` 会按后缀构建镜像；`deploy` 通过仓外 Kustomization 指定标签，不改源码树。独立的 `CAW_K3D_ARTIFACT_DIR` 用于运行输出，不应放进仓库。

三个 k3d 节点仍共享一台物理主机。该演练可观察部署、调度、副本伸缩、滚动更新和 Pod 恢复，不证明物理机 HA、跨故障域恢复、异地备份或生产性能。应把每次运行与实际固定 SHA 和输出目录一起记录。
