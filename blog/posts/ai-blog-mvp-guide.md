---
title: AI 博客系统 MVP 实战
slug: ai-blog-mvp-guide
date: 2026-03-13T00:00:00Z
tags: ai,mvp,blog
status: published
summary: 从零搭建博客展示、AI写作和RAG检索的一体化实践。
---

# AI 博客系统 MVP 实战

## 为什么要三合一

传统博客只能展示内容，而 AI 工作流希望完成内容生产和知识检索。
把展示、写作、检索整合在一个站点后，可以形成闭环：

1. 你写文章或让 AI 写。
2. 文章进入知识库。
3. 后续问题直接用 RAG 从已有内容里检索并分析。

## 架构建议

- `blog/posts`: 已发布文章。
- `blog/drafts`: 草稿，AI 默认写这里。
- `internal/agent`: AI 写作与改稿。
- `internal/rag`: 分块、索引、检索。

## 开发顺序

先做博客读写，再接 AI 写作，最后接 RAG 检索。这样每一步都能独立验收。

## 小结

MVP 阶段最关键的是可运行和可迭代，而不是一次性做到最复杂。
