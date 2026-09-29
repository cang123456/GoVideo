// 上传页交互：管理员登录门禁 + 拖拽 + 进度条 + 多文件顺序上传
// 写成原生 JS 不依赖任何库，省得打包配置。
(function () {
    const zone = document.getElementById('dropZone');
    if (!zone) return;
    const fileInput = document.getElementById('fileInput');
    const listEl = document.getElementById('uploadList');

    const loginBox = document.getElementById('loginBox');
    const uploadZone = document.getElementById('uploadZone');
    const keyInput = document.getElementById('adminKeyInput');
    const loginBtn = document.getElementById('loginBtn');
    const loginMsg = document.getElementById('loginMsg');
    const logoutBtn = document.getElementById('logoutBtn');

    // ---- 登录门禁 ----
    // 先显示登录框（避免上传区闪一下再被藏起来），再问服务端当前的登录状态。
    // 登录凭证放在 HttpOnly Cookie 里，JS 拿不到也不需要拿，浏览器会自动带上。
    showLogin();
    fetch('/api/me')
        .then(r => r.json())
        .then(j => { if (j.loggedIn) showUpload(); })
        .catch(() => { /* 查不到就当未登录，让用户重新验证 */ });

    function showLogin() {
        if (loginBox) loginBox.style.display = '';
        if (uploadZone) uploadZone.style.display = 'none';
        if (keyInput) keyInput.focus();
    }

    function showUpload() {
        if (loginBox) loginBox.style.display = 'none';
        if (uploadZone) uploadZone.style.display = '';
    }

    function setLoginMsg(text) {
        if (loginMsg) loginMsg.textContent = text || '';
    }

    function doLogin() {
        const key = (keyInput.value || '').trim();
        if (!key) {
            setLoginMsg('请输入管理员密钥');
            return;
        }
        loginBtn.disabled = true;
        setLoginMsg('验证中...');
        fetch('/api/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ key: key })
        })
            .then(r => r.json()
                .catch(() => ({ ok: false, msg: '服务返回异常 (HTTP ' + r.status + ')' })))
            .then(j => {
                loginBtn.disabled = false;
                if (j.ok) {
                    keyInput.value = '';
                    setLoginMsg('');
                    showUpload();
                } else {
                    setLoginMsg(j.msg || '验证失败');
                }
            })
            .catch(() => {
                loginBtn.disabled = false;
                setLoginMsg('网络错误，请重试');
            });
    }

    if (loginBtn) loginBtn.addEventListener('click', doLogin);
    if (keyInput) {
        keyInput.addEventListener('keydown', e => {
            if (e.key === 'Enter') doLogin();
        });
    }
    if (logoutBtn) {
        logoutBtn.addEventListener('click', () => {
            fetch('/api/logout', { method: 'POST' })
                .catch(() => {})
                .then(() => location.reload());
        });
    }

    // ---- 拖拽上传 ----

    // 点击区域 = 点隐藏的 input
    zone.addEventListener('click', () => fileInput.click());
    fileInput.addEventListener('change', () => {
        if (fileInput.files.length) handleFiles(fileInput.files);
        fileInput.value = ''; // 清空才能下次再选同一个文件
    });

    // 拖拽高亮
    ['dragenter', 'dragover'].forEach(ev => {
        zone.addEventListener(ev, e => {
            e.preventDefault();
            zone.classList.add('dragover');
        });
    });
    ['dragleave', 'drop'].forEach(ev => {
        zone.addEventListener(ev, e => {
            e.preventDefault();
            zone.classList.remove('dragover');
        });
    });
    zone.addEventListener('drop', e => {
        if (e.dataTransfer.files.length) handleFiles(e.dataTransfer.files);
    });

    function handleFiles(files) {
        for (let i = 0; i < files.length; i++) {
            uploadOne(files[i]);
        }
    }

    function uploadOne(file) {
        const item = document.createElement('div');
        item.className = 'upload-item';
        item.innerHTML = `
            <div class="name"></div>
            <div class="bar"><div class="fill" style="width:0"></div></div>
            <div class="percent">0%</div>
            <div class="status">上传中</div>
        `;
        item.querySelector('.name').textContent = file.name +
            `  (${formatSize(file.size)})`;
        listEl.appendChild(item);

        const fillEl = item.querySelector('.fill');
        const percentEl = item.querySelector('.percent');
        const statusEl = item.querySelector('.status');

        const xhr = new XMLHttpRequest();
        xhr.open('POST', '/api/upload');
        xhr.upload.onprogress = e => {
            if (!e.lengthComputable) return;
            const p = Math.round(e.loaded / e.total * 100);
            fillEl.style.width = p + '%';
            percentEl.textContent = p + '%';
        };
        xhr.onload = () => {
            fillEl.style.width = '100%';
            percentEl.textContent = '100%';

            // 登录过期或被清掉了：回到密钥输入框，别让用户对着"响应错误"发呆
            if (xhr.status === 401) {
                item.remove();
                showLogin();
                setLoginMsg('登录状态已失效，请重新输入管理员密钥');
                return;
            }

            try {
                const json = JSON.parse(xhr.responseText);
                if (json.ok) {
                    statusEl.textContent = '✓ 完成';
                    statusEl.className = 'status ok';
                    // 加一个跳转播放链接
                    const a = document.createElement('a');
                    a.href = '/play/' + json.id;
                    a.textContent = '去播放 →';
                    a.style.marginLeft = '10px';
                    statusEl.textContent = '';
                    statusEl.appendChild(document.createTextNode('✓ '));
                    statusEl.appendChild(a);
                } else {
                    statusEl.textContent = '✗ ' + (json.msg || '失败');
                    statusEl.className = 'status err';
                }
            } catch (e) {
                // 走 nginx 反代时，413/504 等错误返回的是 HTML 页而不是 JSON，
                // 把状态码带出来，一眼能看出是体积超限(413)还是上游超时(504)
                statusEl.textContent = '✗ 响应错误 (HTTP ' + xhr.status + ')';
                statusEl.className = 'status err';
            }
        };
        xhr.onerror = () => {
            statusEl.textContent = '✗ 网络错误';
            statusEl.className = 'status err';
        };
        const fd = new FormData();
        fd.append('file', file);
        xhr.send(fd);
    }

    function formatSize(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        if (bytes < 1024 * 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + ' MB';
        return (bytes / 1024 / 1024 / 1024).toFixed(2) + ' GB';
    }
})();

// 视频卡片的点击跳转（给首页和文件夹页用）
document.addEventListener('click', e => {
    const card = e.target.closest('[data-video-id]');
    if (card) {
        const id = card.getAttribute('data-video-id');
        if (id) location.href = '/play/' + id;
    }
});

// 顶部"重新扫描"按钮（写操作，需要管理员密钥）
document.addEventListener('click', e => {
    const btn = e.target.closest('[data-action=scan]');
    if (!btn) return;
    if (btn.disabled) return;
    btn.disabled = true;
    const old = btn.textContent;
    btn.textContent = '扫描中...';
    fetch('/api/scan', { method: 'POST' })
        .then(r => {
            if (r.status === 401) {
                alert('扫描需要管理员密钥，请先在「上传视频」页面验证');
                location.href = '/upload';
                return null;
            }
            return r.json();
        })
        .then(j => {
            if (!j) return;
            // 简单轮询 5 秒后刷新页面，用户能看到新增
            setTimeout(() => location.reload(), 1500);
        })
        .catch(() => {})
        .finally(() => {
            setTimeout(() => {
                btn.disabled = false;
                btn.textContent = old;
            }, 2000);
        });
});

// 播放页的"删除记录"按钮（写操作，需要管理员密钥）
document.addEventListener('click', e => {
    const btn = e.target.closest('[data-action=delete]');
    if (!btn) return;
    const id = btn.getAttribute('data-id');
    if (!id) return;
    const ok = confirm('确定从数据库删除这条记录吗？(通过网页上传的文件将同时被删除，扫描导入的电影库原文件不会被删)');
    if (!ok) return;

    btn.disabled = true;
    fetch('/api/delete/' + id, { method: 'POST' })
        .then(r => r.json()
            .then(j => ({ status: r.status, body: j }))
            .catch(() => ({ status: r.status, body: {} })))
        .then(res => {
            if (res.body.ok) {
                location.href = '/';
                return;
            }
            btn.disabled = false;
            if (res.status === 401) {
                if (confirm((res.body.msg || '需要管理员密钥') + '\n\n现在去输入密钥吗？')) {
                    location.href = '/upload';
                }
            } else {
                alert(res.body.msg || ('操作失败 (HTTP ' + res.status + ')'));
            }
        })
        .catch(() => {
            btn.disabled = false;
            alert('网络错误');
        });
});
