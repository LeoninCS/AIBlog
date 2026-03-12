---
title: Kubernetes 组件介绍：从控制面到工作节点一文读懂
slug: kubernetes
date: 2026-03-13T01:32:26+08:00
tags: ai,draft
status: published
summary: # Kubernetes 组件介绍：从控制面到工作节点一文读懂  Kubernetes（K8s）是目前最主流的容器编排平台之一。要真正把 K8s 用好，理解它的...
---

# Kubernetes 组件介绍：从控制面到工作节点一文读懂

Kubernetes（K8s）是目前最主流的容器编排平台之一。要真正把 K8s 用好，理解它的“组件分工”非常关键：哪些组件负责“下发指令”，哪些负责“真正运行”，它们之间如何协作，出了问题该看哪里。本文用清晰的结构带你快速建立整体认知。

---

## 1. Kubernetes 架构概览：控制面与数据面

Kubernetes 典型分为两大部分：

- **控制面（Control Plane）**：负责“决策与调度”，管理集群状态，让集群朝“期望状态”收敛。
- **工作节点/数据面（Worker Node / Data Plane）**：负责“执行”，真正运行容器与网络、存储等能力。

一句话理解：**控制面发号施令，工作节点负责干活。**

---

## 2. 控制面组件（Control Plane Components）

控制面通常部署在一组控制节点上（生产环境常为高可用多副本）。

### 2.1 kube-apiserver：集群唯一入口

**作用**：提供 Kubernetes API（REST）服务，是所有组件交互的统一入口。

- 接收 `kubectl`、控制器、调度器等发起的请求  
- 负责认证（AuthN）、鉴权（AuthZ）、准入控制（Admission）
- 所有状态最终都以资源对象形式通过 API Server 写入存储

**理解要点**：  
> 集群里任何“改配置/改状态”的行为，最终都要经过 `kube-apiserver`。

---

### 2.2 etcd：集群的“数据库”

**作用**：保存集群所有关键状态数据（如 Pod、Service、ConfigMap、节点信息等）。

- 强一致性 KV 存储
- 控制面组件通过 API Server 间接读写 etcd
- 对可用性、备份恢复要求很高

**常见建议**：
- etcd 必须定期备份
- 尽量使用独立磁盘、降低延迟

---

### 2.3 kube-scheduler：把 Pod 分配到合适的节点

**作用**：当有新的 Pod 需要运行时，调度器负责选择最合适的 Node。

调度考虑因素包括：

- 资源请求/可用资源（CPU、内存）
- 亲和/反亲和（Affinity/Anti-Affinity）
- 污点与容忍（Taints/Tolerations）
- 拓扑分布、抢占等策略

**理解要点**：  
> 调度器只“决定放哪”，真正“创建并运行”由节点组件完成。

---

### 2.4 kube-controller-manager：集群自动化“控制器集合”

**作用**：运行一组控制器，持续监控资源状态并让其向期望状态收敛。

常见控制器包括：

- Node Controller：节点失联检测与处理
- Deployment/ReplicaSet Controller：副本数管理
- Endpoint Controller：维护 Service 到 Pod 的后端映射
- Job/CronJob Controller：批处理任务管理

**典型行为**：  
用户声明“我要 3 个副本”，控制器发现只有 2 个，就会创建 1 个补齐。

---

### 2.5 cloud-controller-manager（可选）：对接云厂商能力

在公有云环境常见，用于管理云资源联动，例如：

- 负载均衡（LoadBalancer Service）
- 云硬盘挂载（部分场景）
- 节点地址、路由等云特性

---

## 3. 工作节点组件（Worker Node Components）

工作节点是真正承载业务 Pod 的地方。

### 3.1 kubelet：节点上的“管家”

**作用**：负责管理本节点上 Pod 的生命周期，确保容器按期望运行。

- 监听 API Server 的 PodSpec（期望状态）
- 调用容器运行时创建/删除容器
- 上报节点与 Pod 状态（心跳、资源、健康）

**排障提示**：
- Pod 起不来、CrashLoopBackOff 等，常需要看 `kubelet` 日志与事件。

---

### 3.2 容器运行时（Container Runtime）：真正运行容器的引擎

常见运行时：

- `containerd`（当前主流）
- `CRI-O`
- Docker（历史常见，现多通过 containerd 兼容）

Kubelet 通过 **CRI（Container Runtime Interface）** 与运行时交互。

---

### 3.3 kube-proxy：实现 Service 的流量转发规则

**作用**：在节点上维护网络转发规则，使得 Service 能将流量导向后端 Pod。

常见实现方式：

- iptables
- IPVS

**理解要点**：  
> Service 本质是“虚拟入口”，kube-proxy 负责把“入口流量”转到正确的 Pod。

---

### 3.4 CNI 插件：容器网络能力的提供者

Kubernetes 本身不内置具体网络实现，通过 **CNI（Container Network Interface）** 接入网络插件。

常见 CNI：

- Calico（网络策略强）
- Cilium（eBPF 生态）
- Flannel（简单易用）
- Weave 等

CNI 负责：

- Pod IP 分配
- 跨节点通信
- NetworkPolicy（取决于插件能力）

---

### 3.5 CSI 插件：容器存储能力的提供者

通过 **CSI（Container Storage Interface）** 接入存储系统，例如：

- Ceph RBD / CephFS
- 云厂商块存储、文件存储
- NFS（常见但能力有限）

负责动态卷创建、挂载、扩容等操作。

---

## 4. 组件协作流程：创建一个 Pod 会发生什么？

以用户创建一个 Deployment 为例，简化流程如下：

1. 用户通过 `kubectl` 请求 `kube-apiserver`
2. API Server 校验并将期望状态写入 `etcd`
3. Controller 发现需要创建 Pod，生成 Pod 对象
4. Scheduler 为 Pod 选择合适 Node，并写回绑定信息
5. 目标节点的 kubelet 看到新 Pod，调用容器运行时拉镜像并启动
6. CNI 为 Pod 配置网络，kube-proxy 更新 Service 转发规则（若涉及 Service）
7. kubelet 持续上报状态，控制器持续确保副本数与健康

这体现了 Kubernetes 的核心理念：**声明式 + 控制循环（Reconciliation Loop）**。

---

## 5. 小结：记住这几个关键词就够了

- `kube-apiserver`：入口与中枢  
- `etcd`：状态存储  
- `scheduler`：决定放哪里  
- `controller-manager`：持续对齐期望状态  
- `kubelet`：节点执行者  
- `container runtime`：容器真正运行  
- `kube-proxy`：Service 转发规则  
- `CNI/CSI`：网络与存储插件体系  

---

## 结论

Kubernetes 组件并不神秘：控制面负责“决策与收敛”，工作节点负责“执行与承载”。理解每个组件的职责边界与协作流程，不仅能帮你更快上手集群运维，也能在故障排查、性能优化、架构设计时做到有的放矢。

---

如果你愿意，我也可以按你的使用场景继续扩写：例如“面向初学者版本（更通俗）”“面向运维排障版本（附常用命令与日志位置）”，或补充一个“组件对应常见故障与排查思路”的小节。你更想写哪种风格？
