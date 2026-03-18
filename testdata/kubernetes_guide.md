# Kubernetes 核心概念与实践

## 1. 架构概述

Kubernetes（K8s）是一个开源的容器编排平台，用于自动化部署、扩缩和管理容器化应用。其架构分为控制平面（Control Plane）和数据平面（Data Plane）。

### 1.1 控制平面组件

- **kube-apiserver**：Kubernetes API 的入口，所有组件通过 API Server 通信。它提供了 RESTful API，支持认证、授权和准入控制。
- **etcd**：分布式键值存储，保存集群的所有配置数据和状态信息。使用 Raft 一致性算法保证数据一致性。
- **kube-scheduler**：负责将新创建的 Pod 调度到合适的 Node 上。调度决策基于资源需求、亲和性/反亲和性规则、污点和容忍度等因素。
- **kube-controller-manager**：运行各种控制器（Deployment Controller、ReplicaSet Controller、Node Controller 等），通过 watch 机制持续将集群当前状态调谐到期望状态。

### 1.2 数据平面组件

- **kubelet**：运行在每个 Node 上的代理，负责管理 Pod 的生命周期。它从 API Server 获取 PodSpec，确保容器按照 PodSpec 运行。
- **kube-proxy**：运行在每个 Node 上，维护网络规则，实现 Service 的负载均衡。支持 iptables、IPVS 和 userspace 三种代理模式。
- **Container Runtime**：负责运行容器的软件，如 containerd、CRI-O。Kubernetes 通过 CRI（Container Runtime Interface）与容器运行时通信。

## 2. 核心资源对象

### 2.1 Pod

Pod 是 Kubernetes 中最小的部署单元，包含一个或多个容器。同一 Pod 内的容器共享网络命名空间和存储卷。

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nginx-pod
  labels:
    app: nginx
spec:
  containers:
  - name: nginx
    image: nginx:1.25
    ports:
    - containerPort: 80
    resources:
      requests:
        memory: "64Mi"
        cpu: "250m"
      limits:
        memory: "128Mi"
        cpu: "500m"
    livenessProbe:
      httpGet:
        path: /healthz
        port: 80
      initialDelaySeconds: 15
      periodSeconds: 10
    readinessProbe:
      httpGet:
        path: /ready
        port: 80
      initialDelaySeconds: 5
      periodSeconds: 5
```

### 2.2 Deployment

Deployment 管理 ReplicaSet，提供声明式更新能力。它支持滚动更新、回滚和扩缩容。

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.25
```

Deployment 的关键特性：
- **滚动更新**：逐步替换旧版本 Pod，通过 maxSurge 和 maxUnavailable 控制更新速率
- **回滚**：`kubectl rollout undo deployment/nginx-deployment` 可以回滚到上一个版本
- **暂停/恢复**：可以暂停更新以进行多次修改，然后一次性恢复

### 2.3 Service

Service 为一组 Pod 提供稳定的网络端点和负载均衡。

Service 类型：
- **ClusterIP**（默认）：仅在集群内部可访问，分配一个虚拟 IP
- **NodePort**：在每个 Node 上开放一个端口（范围 30000-32767），外部可通过 NodeIP:NodePort 访问
- **LoadBalancer**：创建云厂商的外部负载均衡器，适用于公有云环境
- **ExternalName**：将 Service 映射到外部 DNS 名称

```yaml
apiVersion: v1
kind: Service
metadata:
  name: nginx-service
spec:
  selector:
    app: nginx
  type: ClusterIP
  ports:
  - port: 80
    targetPort: 80
    protocol: TCP
```

### 2.4 ConfigMap 和 Secret

ConfigMap 用于存储非机密的配置数据，Secret 用于存储敏感信息（如密码、令牌、证书）。

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
data:
  database_url: "postgresql://db:5432/myapp"
  log_level: "info"
  feature_flags: |
    enable_new_ui=true
    enable_dark_mode=false
```

## 3. 网络模型

### 3.1 基本原则

Kubernetes 网络模型遵循三个基本原则：
1. 每个 Pod 拥有唯一的 IP 地址
2. 所有 Pod 可以直接通过 IP 地址相互通信（无需 NAT）
3. 容器内看到的 IP 地址与外部其他 Pod 看到的一致

### 3.2 CNI 插件

CNI（Container Network Interface）插件负责实现 Pod 网络。常见插件：
- **Calico**：基于 BGP 的纯三层网络方案，支持网络策略。性能好，适合大规模集群。
- **Flannel**：简单的 overlay 网络方案，通过 VXLAN 或 host-gw 模式实现跨节点通信。
- **Cilium**：基于 eBPF 的网络方案，提供高性能的网络策略和可观测性。

### 3.3 Ingress

Ingress 是管理集群外部访问的 API 对象，通常用于 HTTP/HTTPS 路由。

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: app-ingress
  annotations:
    nginx.ingress.kubernetes.io/rewrite-target: /
spec:
  rules:
  - host: app.example.com
    http:
      paths:
      - path: /api
        pathType: Prefix
        backend:
          service:
            name: api-service
            port:
              number: 8080
      - path: /
        pathType: Prefix
        backend:
          service:
            name: frontend-service
            port:
              number: 80
```

## 4. 存储

### 4.1 Volume 类型

- **emptyDir**：临时存储，Pod 删除时数据丢失。适合缓存或共享文件。
- **hostPath**：挂载 Node 上的文件或目录。适合访问 Node 级别的系统文件。
- **PersistentVolume（PV）**：集群级别的存储资源，生命周期独立于 Pod。
- **PersistentVolumeClaim（PVC）**：用户对存储的请求，绑定到满足要求的 PV。

### 4.2 StorageClass

StorageClass 定义了动态供应存储的方式，允许按需自动创建 PV：

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: fast-ssd
provisioner: pd.csi.storage.gke.io
parameters:
  type: pd-ssd
reclaimPolicy: Delete
allowVolumeExpansion: true
```

## 5. 安全

### 5.1 RBAC

Role-Based Access Control 用于控制对 Kubernetes API 的访问：

- **Role / ClusterRole**：定义一组权限规则
- **RoleBinding / ClusterRoleBinding**：将角色绑定到用户、组或 ServiceAccount

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: pod-reader
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "watch", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: read-pods
subjects:
- kind: ServiceAccount
  name: my-app
roleRef:
  kind: Role
  name: pod-reader
  apiGroup: rbac.authorization.k8s.io
```

### 5.2 Network Policy

NetworkPolicy 用于控制 Pod 之间的网络流量：

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-frontend
spec:
  podSelector:
    matchLabels:
      app: backend
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: frontend
    ports:
    - port: 8080
```

## 6. 可观测性

### 6.1 监控

- **Prometheus + Grafana**：事实上的 Kubernetes 监控标准。Prometheus 采集指标，Grafana 可视化展示。
- **kube-state-metrics**：暴露集群资源对象的状态指标（Deployment、Pod、Node 等）。
- **metrics-server**：提供资源使用指标（CPU、内存），供 HPA 和 kubectl top 使用。

### 6.2 日志

- **Fluentd / Fluent Bit**：日志收集器，将容器日志转发到 Elasticsearch 或其他后端。
- **Loki**：轻量级日志聚合系统，与 Grafana 集成良好。
- **EFK Stack**：Elasticsearch + Fluentd + Kibana，经典的日志解决方案。

### 6.3 分布式追踪

- **Jaeger**：开源的分布式追踪系统，实现了 OpenTracing 标准。
- **OpenTelemetry**：统一的可观测性框架，整合了 OpenTracing 和 OpenCensus。提供 SDK 用于生成 traces、metrics 和 logs。
