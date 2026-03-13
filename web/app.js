const state = {
  posts: [],
  currentPost: null,
  editMode: false,
  postQuery: '',
  inlineSelection: null,
  sidebarCollapsed: false,
};

const el = {
  tabs: document.querySelectorAll('.tab'),
  panels: {
    blog: document.getElementById('tab-blog'),
    writer: document.getElementById('tab-writer'),
    search: document.getElementById('tab-search'),
  },
  healthDot: document.getElementById('health-dot'),
  healthText: document.getElementById('health-text'),
  toggleDrafts: document.getElementById('toggle-drafts'),
  sidebarToggle: document.getElementById('sidebar-toggle'),
  blogLayout: document.getElementById('blog-layout'),
  contentDirectory: document.getElementById('content-directory'),
  postSearchInput: document.getElementById('post-search-input'),
  postSearchBtn: document.getElementById('post-search-btn'),
  postSearchReset: document.getElementById('post-search-reset'),
  postCount: document.getElementById('post-count'),
  postList: document.getElementById('post-list'),
  previewEmpty: document.getElementById('preview-empty'),
  previewContent: document.getElementById('preview-content'),
  previewTitle: document.getElementById('preview-title'),
  previewMeta: document.getElementById('preview-meta'),
  previewTags: document.getElementById('preview-tags'),
  previewHTML: document.getElementById('preview-html'),
  inlineAIBar: document.getElementById('inline-ai-bar'),
  inlineAISelection: document.getElementById('inline-ai-selection'),
  inlineAIPrompt: document.getElementById('inline-ai-prompt'),
  inlineAIRun: document.getElementById('inline-ai-run'),
  editor: document.getElementById('editor'),
  editToggle: document.getElementById('edit-toggle'),
  publishBtn: document.getElementById('publish-btn'),
  deleteBtn: document.getElementById('delete-btn'),
  saveBar: document.getElementById('save-bar'),
  saveBtn: document.getElementById('save-btn'),
  writePrompt: document.getElementById('write-prompt'),
  writeSlug: document.getElementById('write-slug'),
  writeBtn: document.getElementById('write-btn'),
  editSlug: document.getElementById('edit-slug'),
  editPrompt: document.getElementById('edit-prompt'),
  aiEditBtn: document.getElementById('ai-edit-btn'),
  writerLog: document.getElementById('writer-log'),
  searchQuery: document.getElementById('search-query'),
  searchBtn: document.getElementById('search-btn'),
  searchAnswer: document.getElementById('search-answer'),
  searchSources: document.getElementById('search-sources'),
  reindexBtn: document.getElementById('reindex-btn'),
};

init();

function init() {
  restoreSidebarState();
  bindTabs();
  bindEvents();
  healthCheck();
  loadPosts();
}

function bindTabs() {
  el.tabs.forEach((tab) => {
    tab.addEventListener('click', () => {
      el.tabs.forEach((t) => t.classList.remove('active'));
      tab.classList.add('active');
      const key = tab.dataset.tab;
      Object.entries(el.panels).forEach(([name, panel]) => {
        panel.classList.toggle('visible', name === key);
      });
    });
  });
}

function bindEvents() {
  el.toggleDrafts.addEventListener('change', () => loadPosts());
  el.sidebarToggle.addEventListener('click', toggleSidebar);

  el.postSearchBtn.addEventListener('click', () => {
    state.postQuery = el.postSearchInput.value.trim();
    el.postSearchReset.classList.toggle('hidden', !state.postQuery);
    loadPosts();
  });

  el.postSearchInput.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      state.postQuery = el.postSearchInput.value.trim();
      el.postSearchReset.classList.toggle('hidden', !state.postQuery);
      loadPosts();
    }
  });

  el.postSearchReset.addEventListener('click', () => {
    state.postQuery = '';
    el.postSearchInput.value = '';
    el.postSearchReset.classList.add('hidden');
    loadPosts();
  });

  el.editToggle.addEventListener('click', toggleEditMode);
  el.saveBtn.addEventListener('click', saveManualEdit);
  el.publishBtn.addEventListener('click', publishCurrent);
  el.deleteBtn.addEventListener('click', deleteCurrent);
  el.editor.addEventListener('mouseup', captureEditorSelection);
  el.editor.addEventListener('keyup', captureEditorSelection);
  el.inlineAIRun.addEventListener('click', runInlineAIEdit);

  el.writeBtn.addEventListener('click', async () => {
    const text = el.writePrompt.value.trim();
    const slug = el.writeSlug.value.trim();
    if (!text) {
      logWriter('请输入写作需求。');
      return;
    }

    logWriter('正在请求 AI 生成草稿...');
    try {
      const resp = await request('/api/agent/chat', {
        method: 'POST',
        body: JSON.stringify({ mode: 'write', text, slug }),
      });
      logWriter(
        `${resp.reply}\nslug: ${resp.post?.slug ?? '-'}\nfallback: ${resp.fallback}${
          resp.fallback_reason ? `\nfallback_reason: ${resp.fallback_reason}` : ''
        }`,
      );
      await loadPosts();
      if (resp.post?.slug) {
        await selectPost(resp.post.slug);
      }
    } catch (err) {
      logWriter(`写作失败: ${err.message}`);
    }
  });

  el.aiEditBtn.addEventListener('click', async () => {
    const slug = el.editSlug.value.trim();
    const text = el.editPrompt.value.trim();
    if (!slug || !text) {
      logWriter('请填写要改稿的 slug 与改稿要求。');
      return;
    }

    logWriter(`正在对 ${slug} 执行 AI 改稿...`);
    try {
      const resp = await request('/api/agent/chat', {
        method: 'POST',
        body: JSON.stringify({ mode: 'edit', slug, text }),
      });
      logWriter(
        `${resp.reply}\nslug: ${resp.post?.slug ?? '-'}\nfallback: ${resp.fallback}${
          resp.fallback_reason ? `\nfallback_reason: ${resp.fallback_reason}` : ''
        }`,
      );
      await loadPosts();
      await selectPost(slug);
    } catch (err) {
      logWriter(`改稿失败: ${err.message}`);
    }
  });

  el.searchBtn.addEventListener('click', doSearch);
  el.reindexBtn.addEventListener('click', doReindex);
}

async function healthCheck() {
  try {
    await request('/api/health');
    el.healthDot.classList.add('ok');
    el.healthText.textContent = 'Server Online';
  } catch (_) {
    el.healthDot.classList.remove('ok');
    el.healthText.textContent = 'Server Offline';
  }
}

async function loadPosts() {
  const includeDrafts = el.toggleDrafts.checked;
  const query = state.postQuery;
  const params = new URLSearchParams();
  params.set('includeDrafts', String(includeDrafts));
  if (query) {
    params.set('q', query);
  }

  const data = await request(`/api/posts?${params.toString()}`);
  state.posts = data.items || [];

  if (state.currentPost && !state.posts.some((item) => item.slug === state.currentPost.slug)) {
    state.currentPost = null;
    state.editMode = false;
  }

  renderPostList();
  renderCurrentPost();
}

function renderPostList() {
  el.postList.innerHTML = '';
  el.postCount.textContent = `${state.posts.length} 篇内容`;
  if (!state.posts.length) {
    const tip = state.postQuery ? `未找到与「${escapeHtml(state.postQuery)}」相关的文章。` : '暂无文章。你可以先去 AI 写博客生成草稿。';
    el.postList.innerHTML = `<div class="empty-state">${tip}</div>`;
    return;
  }

  state.posts.forEach((post) => {
    const div = document.createElement('div');
    div.className = 'post-item';
    if (state.currentPost && state.currentPost.slug === post.slug) {
      div.classList.add('active');
    }
    const status = escapeHtml(post.status || 'draft');
    const summary = escapeHtml(shorten(getPostPreview(post), 15));
    div.innerHTML = `
      <h4>${escapeHtml(post.title)}</h4>
      <p class="post-summary">${summary}</p>
      <div class="post-meta-row">
        <span class="post-date">${formatDate(post.date)}</span>
        <span class="tag status-${status}">${status}</span>
      </div>
    `;
    div.addEventListener('click', () => selectPost(post.slug));
    el.postList.appendChild(div);
  });
}

function toggleSidebar() {
  state.sidebarCollapsed = !state.sidebarCollapsed;
  syncSidebarState();
}

function restoreSidebarState() {
  try {
    state.sidebarCollapsed = window.localStorage.getItem('aiblog.sidebar.collapsed') === 'true';
  } catch (_) {
    state.sidebarCollapsed = false;
  }
  syncSidebarState();
}

function syncSidebarState() {
  const collapsed = state.sidebarCollapsed;
  el.blogLayout.classList.toggle('sidebar-collapsed', collapsed);
  el.contentDirectory.classList.toggle('collapsed', collapsed);
  el.sidebarToggle.innerHTML = `<span aria-hidden="true">${collapsed ? '→' : '←'}</span>`;
  el.sidebarToggle.setAttribute('aria-expanded', String(!collapsed));
  el.sidebarToggle.setAttribute('aria-label', collapsed ? '展开目录' : '收起目录');
  el.sidebarToggle.setAttribute('title', collapsed ? '展开目录' : '收起目录');
  try {
    window.localStorage.setItem('aiblog.sidebar.collapsed', String(collapsed));
  } catch (_) {
    // no-op
  }
}

async function selectPost(slug) {
  const post = await request(`/api/posts/${encodeURIComponent(slug)}`);
  state.currentPost = post;
  state.editMode = false;
  renderCurrentPost();
  renderPostList();
}

function renderCurrentPost() {
  const p = state.currentPost;
  if (!p) {
    el.previewEmpty.classList.remove('hidden');
    el.previewContent.classList.add('hidden');
    return;
  }

  el.previewEmpty.classList.add('hidden');
  el.previewContent.classList.remove('hidden');
  el.previewTitle.textContent = p.title || p.slug;
  el.previewMeta.textContent = `${formatDate(p.date)} · ${p.status || 'draft'} · ${p.slug}`;
  renderPreviewTags(p.tags || []);
  el.publishBtn.classList.toggle('hidden', (p.status || '').toLowerCase() === 'published');
  el.editor.value = p.body || '';
  el.previewHTML.innerHTML = p.html || '';
  resetInlineAI();

  if (state.editMode) {
    el.editor.classList.remove('hidden');
    el.previewHTML.classList.add('hidden');
    el.inlineAIBar.classList.remove('hidden');
    el.saveBar.classList.remove('hidden');
    el.editToggle.textContent = '取消编辑';
  } else {
    el.editor.classList.add('hidden');
    el.previewHTML.classList.remove('hidden');
    el.inlineAIBar.classList.add('hidden');
    el.saveBar.classList.add('hidden');
    el.editToggle.textContent = '编辑';
  }
}

function renderPreviewTags(tags) {
  if (!tags.length) {
    el.previewTags.innerHTML = '';
    el.previewTags.classList.add('hidden');
    return;
  }

  el.previewTags.innerHTML = tags.map((tag) => `<span class="tag">${escapeHtml(tag)}</span>`).join('');
  el.previewTags.classList.remove('hidden');
}

function toggleEditMode() {
  if (!state.currentPost) return;
  state.editMode = !state.editMode;
  renderCurrentPost();
}

function captureEditorSelection() {
  if (!state.editMode) return;

  const start = el.editor.selectionStart;
  const end = el.editor.selectionEnd;
  if (start == null || end == null || start === end) {
    state.inlineSelection = null;
    el.inlineAISelection.textContent = '选中文字后，可直接让 AI 帮你改写。';
    return;
  }

  const selected = el.editor.value.slice(start, end);
  state.inlineSelection = { start, end, selected };
  el.inlineAISelection.textContent = `已选中 ${selected.length} 个字符：${shorten(selected.replace(/\s+/g, ' ').trim(), 42)}`;
}

async function runInlineAIEdit() {
  if (!state.currentPost || !state.editMode) return;
  if (!state.inlineSelection?.selected) {
    alert('请先在编辑器里选中一段文本。');
    return;
  }

  const prompt = el.inlineAIPrompt.value.trim();
  if (!prompt) {
    alert('请先输入你希望 AI 如何修改这段内容。');
    return;
  }

  const { start, end, selected } = state.inlineSelection;
  const context = buildInlineContext(el.editor.value, start, end);
  const originalLabel = el.inlineAIRun.textContent;
  el.inlineAIRun.disabled = true;
  el.inlineAIRun.textContent = 'AI 修改中...';

  try {
    const resp = await request('/api/agent/chat', {
      method: 'POST',
      body: JSON.stringify({
        mode: 'inline-edit',
        slug: state.currentPost.slug,
        text: prompt,
        selected,
        context,
      }),
    });

    replaceEditorSelection(start, end, (resp.reply || selected).trim());
    el.inlineAIPrompt.value = '';
  } catch (err) {
    alert(`AI 修改失败: ${err.message}`);
  } finally {
    el.inlineAIRun.disabled = false;
    el.inlineAIRun.textContent = originalLabel;
  }
}

function replaceEditorSelection(start, end, replacement) {
  const value = el.editor.value;
  el.editor.value = `${value.slice(0, start)}${replacement}${value.slice(end)}`;
  const nextEnd = start + replacement.length;
  el.editor.focus();
  el.editor.setSelectionRange(start, nextEnd);
  state.inlineSelection = {
    start,
    end: nextEnd,
    selected: replacement,
  };
  el.inlineAISelection.textContent = `AI 已替换选中内容，共 ${replacement.length} 个字符。`;
}

function buildInlineContext(text, start, end) {
  const before = text.slice(Math.max(0, start - 240), start).trim();
  const after = text.slice(end, Math.min(text.length, end + 240)).trim();
  return `前文：\n${before}\n\n后文：\n${after}`.trim();
}

function resetInlineAI() {
  state.inlineSelection = null;
  el.inlineAIPrompt.value = '';
  el.inlineAISelection.textContent = '选中文字后，可直接让 AI 帮你改写。';
}

async function saveManualEdit() {
  if (!state.currentPost) return;

  const payload = {
    title: state.currentPost.title,
    slug: state.currentPost.slug,
    date: state.currentPost.date,
    tags: state.currentPost.tags,
    status: state.currentPost.status,
    summary: state.currentPost.summary,
    body: el.editor.value,
  };

  try {
    const saved = await request(`/api/posts/${encodeURIComponent(state.currentPost.slug)}`, {
      method: 'PUT',
      headers: {
        'If-Match': state.currentPost.version || '',
      },
      body: JSON.stringify(payload),
    });
    state.currentPost = saved;
    state.editMode = false;
    renderCurrentPost();
    await loadPosts();
  } catch (err) {
    alert(`保存失败: ${err.message}`);
  }
}

async function publishCurrent() {
  if (!state.currentPost) return;

  try {
    const published = await request(`/api/posts/${encodeURIComponent(state.currentPost.slug)}/publish`, {
      method: 'POST',
      headers: {
        'If-Match': state.currentPost.version || '',
      },
    });
    state.currentPost = published;
    renderCurrentPost();
    await loadPosts();
  } catch (err) {
    alert(`发布失败: ${err.message}`);
  }
}

async function deleteCurrent() {
  if (!state.currentPost) return;

  const slug = state.currentPost.slug;
  const confirmed = window.confirm(`确定删除文章 ${slug} 吗？该操作不可撤销。`);
  if (!confirmed) return;

  try {
    await request(`/api/posts/${encodeURIComponent(slug)}`, {
      method: 'DELETE',
      headers: {
        'If-Match': state.currentPost.version || '',
      },
    });
    state.currentPost = null;
    state.editMode = false;
    await loadPosts();
  } catch (err) {
    alert(`删除失败: ${err.message}`);
  }
}

async function doSearch() {
  const query = el.searchQuery.value.trim();
  if (!query) return;

  el.searchAnswer.textContent = '检索中...';
  el.searchSources.innerHTML = '';

  try {
    const data = await request('/api/rag/query', {
      method: 'POST',
      body: JSON.stringify({ query }),
    });
    el.searchAnswer.textContent = data.answer || '无结果';
    renderSources(data.chunks || []);
  } catch (err) {
    el.searchAnswer.textContent = `检索失败: ${err.message}`;
  }
}

async function doReindex() {
  try {
    const data = await request('/api/rag/reindex', { method: 'POST' });
    el.searchAnswer.textContent = `索引重建完成，chunks: ${data.chunks}`;
  } catch (err) {
    el.searchAnswer.textContent = `重建失败: ${err.message}`;
  }
}

function renderSources(chunks) {
  if (!chunks.length) {
    el.searchSources.innerHTML = '<div class="empty-state">暂无引用来源。</div>';
    return;
  }

  el.searchSources.innerHTML = chunks
    .map(
      (c, idx) => `
      <div class="sources-item">
        <div class="source-title">
          <strong>${idx + 1}. ${escapeHtml(c.title || c.slug)}</strong>
          <span class="source-score">score ${(c.score || 0).toFixed(2)}</span>
        </div>
        <div class="source-path">${escapeHtml(c.path || '')}</div>
        <p class="source-text">${escapeHtml(shorten(c.text || '', 240))}</p>
      </div>
    `,
    )
    .join('');
}

function logWriter(text) {
  const now = new Date().toLocaleTimeString();
  el.writerLog.textContent = `[${now}] ${text}\n${el.writerLog.textContent || ''}`.trim();
}

async function request(url, options = {}) {
  const res = await fetch(url, {
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    },
    ...options,
  });

  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    try {
      const data = await res.json();
      if (data.error) {
        message = data.error;
      }
    } catch (_) {
      // no-op
    }
    throw new Error(message);
  }

  if (res.status === 204) {
    return null;
  }
  return res.json();
}

function formatDate(dateStr) {
  if (!dateStr) return '-';
  const date = new Date(dateStr);
  if (Number.isNaN(date.getTime())) return dateStr;
  return date.toLocaleDateString();
}

function shorten(text, n) {
  if (text.length <= n) return text;
  return `${text.slice(0, n)}...`;
}

function getPostPreview(post) {
  const summary = normalizePreviewText(post.summary || '');
  if (summary) {
    return summary;
  }
  return '暂无简介';
}

function normalizePreviewText(input) {
  if (!input) return '';
  let text = String(input)
    .replace(/^---[\s\S]*?---/, ' ')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/^>\s?/gm, ' ')
    .replace(/^\s*[-=]{3,}\s*$/gm, ' ')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\*([^*]+)\*/g, '$1')
    .replace(/__([^_]+)__/g, '$1')
    .replace(/_([^_]+)_/g, '$1')
    .replace(/~~([^~]+)~~/g, '$1')
    .replace(/!\[[^\]]*]\([^)]+\)/g, ' ')
    .replace(/\[[^\]]+]\([^)]+\)/g, '$1')
    .replace(/^#{1,6}\s+/gm, ' ')
    .replace(/^\s*\[[ xX]\]\s+/gm, ' ')
    .replace(/^\s*[-*+]\s+/gm, ' ')
    .replace(/^\s*\d+\.\s+/gm, ' ')
    .replace(/^\s*\d+\)\s+/gm, ' ')
    .replace(/\|/g, ' ')
    .replace(/[>#*_~]+/g, ' ')
    .replace(/\n+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();

  const firstSentence = text.split(/[。！？!?]/).find((part) => part.trim().length > 0);
  if (firstSentence && firstSentence.trim().length >= 10) {
    text = firstSentence.trim();
  }
  return text;
}

function escapeHtml(input) {
  return String(input)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}
