package server

const modUpdatePageHTML = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>DMP 模组一键更新</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #1a1a2e; color: #eee; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
.card { background: #16213e; border-radius: 16px; padding: 32px; width: 420px; box-shadow: 0 8px 32px rgba(0,0,0,0.3); }
h1 { font-size: 22px; margin-bottom: 24px; text-align: center; color: #e94560; }
.form-group { margin-bottom: 16px; }
label { display: block; font-size: 13px; color: #999; margin-bottom: 6px; }
input { width: 100%; padding: 10px 14px; border: 1px solid #333; border-radius: 8px; background: #0f3460; color: #eee; font-size: 14px; outline: none; }
input:focus { border-color: #e94560; }
.btn { width: 100%; padding: 12px; border: none; border-radius: 8px; background: #e94560; color: white; font-size: 16px; font-weight: 600; cursor: pointer; transition: background 0.2s; margin-top: 8px; }
.btn:hover { background: #c73550; }
.btn:disabled { background: #555; cursor: not-allowed; }
.result { margin-top: 16px; padding: 14px; border-radius: 8px; font-size: 14px; display: none; }
.result.success { background: #1b4332; border: 1px solid #2d6a4f; display: block; }
.result.error { background: #4a1525; border: 1px solid #6b2040; display: block; }
.log { margin-top: 16px; max-height: 200px; overflow-y: auto; font-size: 12px; color: #888; font-family: monospace; }
.log p { margin: 2px 0; }
.log .ok { color: #52b788; }
.log .fail { color: #e94560; }
.spinner { display: inline-block; width: 16px; height: 16px; border: 2px solid #fff; border-top-color: transparent; border-radius: 50%; animation: spin 0.6s linear infinite; vertical-align: middle; margin-right: 8px; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
</head>
<body>
<div class="card">
<h1>DMP 模组一键更新</h1>
<div class="form-group">
<label>用户名</label>
<input id="username" placeholder="输入DMP用户名" value="">
</div>
<div class="form-group">
<label>密码</label>
<input id="password" type="password" placeholder="输入DMP密码">
</div>
<div class="form-group">
<label>房间 ID</label>
<input id="roomID" type="number" placeholder="房间ID，如 4" value="4">
</div>
<button class="btn" id="updateBtn" onclick="updateAll()">一键更新所有模组</button>
<div class="result" id="result"></div>
<div class="log" id="log"></div>
</div>
<script>
function addLog(msg, type) {
  const log = document.getElementById('log');
  const p = document.createElement('p');
  p.className = type || '';
  p.textContent = msg;
  log.appendChild(p);
  log.scrollTop = log.scrollHeight;
}

async function updateAll() {
  const btn = document.getElementById('updateBtn');
  const result = document.getElementById('result');
  const log = document.getElementById('log');
  const username = document.getElementById('username').value.trim();
  const password = document.getElementById('password').value.trim();
  const roomID = parseInt(document.getElementById('roomID').value);

  if (!username || !password) {
    result.className = 'result error';
    result.textContent = '请输入用户名和密码';
    return;
  }
  if (!roomID) {
    result.className = 'result error';
    result.textContent = '请输入房间 ID';
    return;
  }

  btn.disabled = true;
  btn.innerHTML = '<span class="spinner"></span>正在更新...';
  result.className = 'result';
  log.innerHTML = '';

  try {
    // 登录获取 token
    addLog('正在登录...');
    const loginRes = await fetch('/v3/user/login', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({username, password})
    });
    const loginData = await loginRes.json();
    if (loginData.code !== 200 || !loginData.data?.token) {
      throw new Error(loginData.message || '登录失败');
    }
    const token = loginData.data.token;
    addLog('登录成功', 'ok');

    // 调用更新接口
    addLog('正在更新模组（可能需要几分钟）...');
    const updateRes = await fetch('/v3/mod/update_all', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-DMP-TOKEN': token
      },
      body: JSON.stringify({roomID})
    });
    const updateData = await updateRes.json();

    if (updateData.code === 200) {
      result.className = 'result success';
      result.textContent = updateData.message;
      addLog(updateData.message, 'ok');
      if (updateData.data?.fail > 0) {
        addLog('失败的模组 ID: ' + (updateData.data.failIDs || []).join(', '), 'fail');
      }
    } else {
      result.className = 'result error';
      result.textContent = updateData.message;
      addLog(updateData.message, 'fail');
    }
  } catch (e) {
    result.className = 'result error';
    result.textContent = e.message;
    addLog(e.message, 'fail');
  }

  btn.disabled = false;
  btn.innerHTML = '一键更新所有模组';
}
</script>
</body>
</html>`
