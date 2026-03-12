const state = {
  posts: [],
  currentPost: null,
  editMode: false,
  postQuery: '',
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
  postSearchInput: document.getElementById('post-search-input'),
  postSearchBtn: document.getElementById('post-search-btn'),
  postSearchReset: document.getElementById('post-search-reset'),
  postList: document.getElementById('post-list'),
  previewEmpty: document.getElementById('preview-empty'),
  previewContent: document.getElementById('preview-content'),
  previewTitle: document.getElementById('preview-title'),
  previewMeta: document.getElementById('preview-meta'),
  previewHTML: document.getElementById('preview-html'),
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
      logWriter(`${resp.reply}\nslug: ${resp.post?.slug ?? '-'}\nfallback: ${resp.fallback}`);
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
      logWriter(`${resp.reply}\nslug: ${resp.post?.slug ?? '-'}\nfallback: ${resp.fallback}`);
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
    div.innerHTML = `
      <h4>${escapeHtml(post.title)}</h4>
      <p>${escapeHtml(post.summary || 'No summary')}</p>
      <p>${formatDate(post.date)} · ${post.status || 'draft'}</p>
    `;
    div.addEventListener('click', () => selectPost(post.slug));
    el.postList.appendChild(div);
  });
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
  el.editor.value = p.body || '';
  el.previewHTML.innerHTML = p.html || '';

  if (state.editMode) {
    el.editor.classList.remove('hidden');
    el.previewHTML.classList.add('hidden');
    el.saveBar.classList.remove('hidden');
    el.editToggle.textContent = '取消编辑';
  } else {
    el.editor.classList.add('hidden');
    el.previewHTML.classList.remove('hidden');
    el.saveBar.classList.add('hidden');
    el.editToggle.textContent = '编辑';
  }
}

function toggleEditMode() {
  if (!state.currentPost) return;
  state.editMode = !state.editMode;
  renderCurrentPost();
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
        <strong>${idx + 1}. ${escapeHtml(c.title || c.slug)}</strong>
        <div>path: ${escapeHtml(c.path || '')} · score: ${(c.score || 0).toFixed(2)}</div>
        <p>${escapeHtml(shorten(c.text || '', 240))}</p>
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

function escapeHtml(input) {
  return String(input)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}
