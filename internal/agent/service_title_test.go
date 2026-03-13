package agent

import "testing"

func TestExtractSuggestedTitleSupportsStructuredBriefFormats(t *testing.T) {
	t.Run("ordered list", func(t *testing.T) {
		brief := `
1. 文章目标：解释 Kubernetes 核心组件的职责与协作方式。
2. 目标读者：刚接触 K8s 的工程师。
3. 建议标题: Kubernetes 核心组件详解
4. 建议结构：导语 -> 控制平面 -> 工作节点 -> 总结。
`
		if got := extractSuggestedTitle(brief); got != "Kubernetes 核心组件详解" {
			t.Fatalf("extractSuggestedTitle() = %q, want %q", got, "Kubernetes 核心组件详解")
		}
	})

	t.Run("markdown bullets", func(t *testing.T) {
		brief := `
- **文章目标**：帮助读者快速理解 Pod、Scheduler 与 kubelet 的关系。
- **建议标题**：
  K8s 组件协作机制详解
- **写作语气**：专业、清晰、克制。
`
		if got := extractSuggestedTitle(brief); got != "K8s 组件协作机制详解" {
			t.Fatalf("extractSuggestedTitle() = %q, want %q", got, "K8s 组件协作机制详解")
		}
	})
}

func TestResolvedArticleTitleRejectsPlaceholder(t *testing.T) {
	brief := `
文章目标：围绕 Kubernetes 组件写一篇结构清晰的技术博客。
目标读者：想快速建立整体认知的技术读者。
建议结构：导语 -> 核心概念 -> 实践要点 -> 总结。
`

	got := resolvedArticleTitle("AI 博客草稿", brief, "写一篇关于K8s组件博客的详细介绍")
	want := "K8s组件详解"
	if got != want {
		t.Fatalf("resolvedArticleTitle() = %q, want %q", got, want)
	}
}

func TestNormalizeTitleStripsLabelsAndFormatting(t *testing.T) {
	got := normalizeTitle("**建议标题：** Kubernetes 控制平面详解\n补充说明")
	want := "Kubernetes 控制平面详解"
	if got != want {
		t.Fatalf("normalizeTitle() = %q, want %q", got, want)
	}
}
