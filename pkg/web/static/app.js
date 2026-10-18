// State
let currentTab = 'dashboard';
let userPage = 1;
let profilesList = [];
let searchTimeout = null;

// Initialization
document.addEventListener('DOMContentLoaded', () => {
  setupTabs();
  loadStats();
  loadProfiles();
  loadLiveSessions();
  updateScript();

  // Periodic polling for real-time dashboard updates
  setInterval(() => {
    loadStats();
    if (currentTab === 'sessions' || currentTab === 'dashboard') {
      loadLiveSessions();
    }
  }, 4000);
});

// Tab Switching
function setupTabs() {
  document.querySelectorAll('.nav-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const tab = btn.getAttribute('data-tab');
      switchTab(tab);
    });
  });
}

function switchTab(tabName) {
  currentTab = tabName;
  document.querySelectorAll('.nav-btn').forEach(b => b.classList.remove('active'));
  document.querySelectorAll('.tab-pane').forEach(p => p.classList.remove('active'));

  const activeBtn = document.querySelector(`.nav-btn[data-tab="${tabName}"]`);
  const activePane = document.getElementById(`tab-${tabName}`);
  if (activeBtn) activeBtn.classList.add('active');
  if (activePane) activePane.classList.add('active');

  // Trigger data load
  switch (tabName) {
    case 'users':
      loadUsers(userPage);
      break;
    case 'sessions':
      loadLiveSessions();
      break;
    case 'profiles':
      loadProfiles();
      break;
    case 'nas':
      loadNAS();
      break;
    case 'logs':
      loadLogs(0);
      break;
    case 'batch':
      populateBatchProfiles();
      break;
  }
}

// Format Utilities
function formatBytes(bytes) {
  if (!bytes || bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

function formatDuration(seconds) {
  if (!seconds) return '0s';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) return `${h}h ${m}m ${s}s`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

// 1. Stats
async function loadStats() {
  try {
    const res = await fetch('/api/stats');
    if (!res.ok) return;
    const s = await res.json();

    document.getElementById('statTotalUsers').innerText = s.total_users.toLocaleString();
    document.getElementById('statActiveUsers').innerText = `${s.active_users.toLocaleString()} نشط`;
    document.getElementById('statOnlineSessions').innerText = s.online_sessions.toLocaleString();
    document.getElementById('statTotalDL').innerText = formatBytes(s.total_download);
    document.getElementById('statTotalUL').innerText = formatBytes(s.total_upload);
    document.getElementById('statTotalNAS').innerText = s.total_nas.toLocaleString();
    document.getElementById('statDBSize').innerText = formatBytes(s.db_size_bytes);

    document.getElementById('userCountBadge').innerText = s.total_users;
    document.getElementById('sessionCountBadge').innerText = s.online_sessions;
    document.getElementById('uptimeText').innerText = `متصل منذ ${formatDuration(s.uptime_seconds)}`;
  } catch (err) {
    console.error('Failed to load stats', err);
  }
}

// 2. Users Management
async function loadUsers(page = 1) {
  userPage = page;
  const search = document.getElementById('userSearchInput').value.trim();
  const status = document.getElementById('userStatusFilter').value;

  const tbody = document.getElementById('usersTableBody');
  tbody.innerHTML = `<tr><td colspan="10" class="text-center">جاري جلب المشتركين...</td></tr>`;

  try {
    const url = `/api/users?page=${page}&pageSize=50&search=${encodeURIComponent(search)}&status=${encodeURIComponent(status)}`;
    const res = await fetch(url);
    const data = await res.json();

    if (!data.users || data.users.length === 0) {
      tbody.innerHTML = `<tr><td colspan="10" class="text-center text-muted">لا يوجد مشتركين مطابقين</td></tr>`;
      renderPagination('userPagination', 0, 50, 1, loadUsers);
      return;
    }

    tbody.innerHTML = data.users.map((u, idx) => {
      const rowNum = (data.page - 1) * data.page_size + idx + 1;
      const statusBadge = `<span class="badge badge-${u.status}">${u.status === 'active' ? 'نشط' : (u.status === 'expired' ? 'منتهي' : 'معطل')}</span>`;
      const speedBadge = u.rate_limit ? `<span class="badge badge-speed">${u.rate_limit}</span>` : '-';
      const quotaText = u.data_limit_bytes > 0 ? `${formatBytes(u.used_data_bytes)} / ${formatBytes(u.data_limit_bytes)}` : `${formatBytes(u.used_data_bytes)} / ∞`;
      const expText = u.expiration_date ? new Date(u.expiration_date).toLocaleDateString('ar-EG') : 'دائم';

      return `
        <tr>
          <td>${rowNum}</td>
          <td><strong>${escapeHtml(u.username)}</strong></td>
          <td><code>${escapeHtml(u.password)}</code></td>
          <td>${speedBadge}</td>
          <td>${u.ip_address || '-'}</td>
          <td>${u.mac_address || '-'}</td>
          <td>${statusBadge}</td>
          <td>${quotaText}</td>
          <td>${expText}</td>
          <td>
            <button class="btn btn-sm btn-outline" onclick='openEditUserModal(${JSON.stringify(u)})'>تعديل</button>
            <button class="btn btn-sm btn-danger" onclick="deleteUser(${u.id}, '${escapeHtml(u.username)}')">حذف</button>
          </td>
        </tr>
      `;
    }).join('');

    renderPagination('userPagination', data.total_count, data.page_size, data.page, loadUsers);
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="10" class="text-center text-muted">خطأ في جلب البيانات: ${err.message}</td></tr>`;
  }
}

function handleUserSearch(e) {
  clearTimeout(searchTimeout);
  searchTimeout = setTimeout(() => {
    loadUsers(1);
  }, 350);
}

function openAddUserModal() {
  document.getElementById('userModalTitle').innerText = 'إضافة مشترك جديد';
  document.getElementById('userId').value = '0';
  document.getElementById('formUsername').value = '';
  document.getElementById('formPassword').value = Math.random().toString(36).slice(-6);
  document.getElementById('formIP').value = '';
  document.getElementById('formMAC').value = '';
  document.getElementById('formDataLimit').value = '0';
  document.getElementById('formExpiration').value = '';
  document.getElementById('formStatus').value = 'active';

  populateProfileSelect('formProfileId');
  openModal('userModal');
}

function openEditUserModal(u) {
  document.getElementById('userModalTitle').innerText = `تعديل المشترك: ${u.username}`;
  document.getElementById('userId').value = u.id;
  document.getElementById('formUsername').value = u.username;
  document.getElementById('formPassword').value = u.password;
  document.getElementById('formIP').value = u.ip_address || '';
  document.getElementById('formMAC').value = u.mac_address || '';
  document.getElementById('formDataLimit').value = u.data_limit_bytes ? Math.floor(u.data_limit_bytes / (1024*1024*1024)) : 0;
  document.getElementById('formStatus').value = u.status;

  if (u.expiration_date) {
    document.getElementById('formExpiration').value = u.expiration_date.split('T')[0];
  } else {
    document.getElementById('formExpiration').value = '';
  }

  populateProfileSelect('formProfileId', u.profile_id);
  openModal('userModal');
}

async function saveUserForm(e) {
  e.preventDefault();
  const id = parseInt(document.getElementById('userId').value);
  const dataLimitGB = parseInt(document.getElementById('formDataLimit').value) || 0;
  const expVal = document.getElementById('formExpiration').value;

  const payload = {
    id: id,
    username: document.getElementById('formUsername').value.trim(),
    password: document.getElementById('formPassword').value.trim(),
    profile_id: parseInt(document.getElementById('formProfileId').value) || 0,
    status: document.getElementById('formStatus').value,
    ip_address: document.getElementById('formIP').value.trim(),
    mac_address: document.getElementById('formMAC').value.trim(),
    data_limit_bytes: dataLimitGB * 1024 * 1024 * 1024,
    expiration_date: expVal ? new Date(expVal).toISOString() : null
  };

  try {
    const res = await fetch('/api/users', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    if (!res.ok) {
      const err = await res.json();
      alert('خطأ: ' + (err.error || 'فشل الحفظ'));
      return;
    }
    closeModal('userModal');
    loadUsers(userPage);
    loadStats();
  } catch (err) {
    alert('فشل الاتصال بالسيرفر: ' + err.message);
  }
}

async function deleteUser(id, username) {
  if (!confirm(`هل أنت متأكد من حذف المشترك "${username}"؟`)) return;
  try {
    await fetch(`/api/users?id=${id}`, { method: 'DELETE' });
    loadUsers(userPage);
    loadStats();
  } catch (err) {
    alert('فشل الحذف: ' + err.message);
  }
}

// 3. Batch Generator
async function handleBatchGenerate(e) {
  e.preventDefault();
  const btn = document.getElementById('btnBatchSubmit');
  const notice = document.getElementById('batchResultNotice');
  btn.disabled = true;
  btn.innerText = 'جاري التوليد وحفظ الكروت بالذاكرة وقاعدة البيانات...';

  const payload = {
    count: parseInt(document.getElementById('batchCount').value),
    prefix: document.getElementById('batchPrefix').value.trim(),
    user_length: parseInt(document.getElementById('batchLen').value),
    profile_id: parseInt(document.getElementById('batchProfileSelect').value),
    data_limit_gb: parseInt(document.getElementById('batchDataLimit').value) || 0,
    validity_days: parseInt(document.getElementById('batchDays').value) || 0
  };

  try {
    const res = await fetch('/api/users/batch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (res.ok && data.success) {
      notice.className = 'alert alert-success mt-3';
      notice.innerText = `تم توليد وتهيئة ${data.created.toLocaleString()} كارت بنجاح فائق وتم تحديث ذاكرة الراديوس!`;
      notice.style.display = 'block';
      loadStats();
    } else {
      notice.className = 'alert alert-danger mt-3';
      notice.innerText = 'خطأ أثناء التوليد: ' + (data.error || 'حدث خطأ');
      notice.style.display = 'block';
    }
  } catch (err) {
    notice.className = 'alert alert-danger mt-3';
    notice.innerText = 'فشل الاتصال: ' + err.message;
    notice.style.display = 'block';
  } finally {
    btn.disabled = false;
    btn.innerText = 'توليد الكروت فوراً';
  }
}

function populateBatchProfiles() {
  populateProfileSelect('batchProfileSelect');
}

// 4. Live Sessions & CoA Disconnect
async function loadLiveSessions() {
  try {
    const res = await fetch('/api/sessions');
    if (!res.ok) return;
    const sessions = await res.json();

    const renderRows = (sList, tableId, isFull = false) => {
      const tbody = document.getElementById(tableId);
      if (!tbody) return;
      if (!sList || sList.length === 0) {
        tbody.innerHTML = `<tr><td colspan="${isFull ? 9 : 8}" class="text-center text-muted">لا يوجد جلسات نشطة حالياً</td></tr>`;
        return;
      }

      tbody.innerHTML = sList.map(s => {
        return `
          <tr>
            <td><strong>${escapeHtml(s.username)}</strong></td>
            ${isFull ? `<td><code>${escapeHtml(s.acctsessionid)}</code></td>` : ''}
            <td><span class="badge badge-speed">${s.framedipaddress || '0.0.0.0'}</span></td>
            <td><code>${s.callingstationid || '-'}</code></td>
            <td>${s.nasipaddress}</td>
            <td>${formatDuration(s.acctsessiontime)}</td>
            <td>${formatBytes(s.acctinputoctets)}</td>
            <td>${formatBytes(s.acctoutputoctets)}</td>
            <td>
              <button class="btn btn-sm btn-danger" onclick="disconnectSession('${s.nasipaddress}', '${escapeHtml(s.username)}', '${escapeHtml(s.acctsessionid)}', '${s.framedipaddress}')">
                قطع الاتصال (PoD)
              </button>
            </td>
          </tr>
        `;
      }).join('');
    };

    renderRows(sessions, 'quickSessionsTable', false);
    renderRows(sessions, 'fullSessionsTable', true);
  } catch (err) {
    console.error('Failed to load sessions', err);
  }
}

async function disconnectSession(nasIP, username, sessionID, framedIP) {
  if (!confirm(`هل أنت متأكد من قطع اتصال المشترك "${username}" وإرسال أمر Disconnect (PoD) للمايكروتك؟`)) return;

  try {
    const res = await fetch('/api/disconnect', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        nas_ip: nasIP,
        username: username,
        session_id: sessionID,
        framed_ip: framedIP
      })
    });
    const data = await res.json();
    alert(data.message || (data.success ? 'تم قطع الاتصال بنجاح' : 'فشل قطع الاتصال'));
    loadLiveSessions();
    loadStats();
  } catch (err) {
    alert('فشل إرسال أمر الـ Disconnect: ' + err.message);
  }
}

// 5. Profiles Management
async function loadProfiles() {
  const tbody = document.getElementById('profilesTableBody');
  try {
    const res = await fetch('/api/profiles');
    profilesList = await res.json();

    if (tbody) {
      if (!profilesList || profilesList.length === 0) {
        tbody.innerHTML = `<tr><td colspan="9" class="text-center text-muted">لا يوجد باقات مضافة</td></tr>`;
        return;
      }
      tbody.innerHTML = profilesList.map(p => `
        <tr>
          <td>${p.id}</td>
          <td><strong>${escapeHtml(p.name)}</strong></td>
          <td><span class="badge badge-speed">${escapeHtml(p.rate_limit)}</span></td>
          <td>${escapeHtml(p.pool_name) || '-'}</td>
          <td>${formatDuration(p.session_timeout)}</td>
          <td>${formatDuration(p.idle_timeout)}</td>
          <td>${escapeHtml(p.mikrotik_address_list) || '-'}</td>
          <td><b>${p.price} ج.م</b></td>
          <td>
            <button class="btn btn-sm btn-outline" onclick='openEditProfileModal(${JSON.stringify(p)})'>تعديل</button>
            <button class="btn btn-sm btn-danger" onclick="deleteProfile(${p.id})">حذف</button>
          </td>
        </tr>
      `).join('');
    }
  } catch (err) {
    console.error('Failed to load profiles', err);
  }
}

function populateProfileSelect(selectId, selectedId = 0) {
  const sel = document.getElementById(selectId);
  if (!sel) return;
  sel.innerHTML = profilesList.map(p => `
    <option value="${p.id}" ${p.id === selectedId ? 'selected' : ''}>
      ${escapeHtml(p.name)} (${p.rate_limit || 'بدون تحديد'})
    </option>
  `).join('');
}

function openAddProfileModal() {
  document.getElementById('profileModalTitle').innerText = 'إضافة باقة جديدة';
  document.getElementById('profileId').value = '0';
  document.getElementById('profileName').value = '';
  document.getElementById('profileRateLimit').value = '5M/10M';
  document.getElementById('profilePoolName').value = 'pppoe-pool';
  document.getElementById('profileSessionTimeout').value = '86400';
  document.getElementById('profileIdleTimeout').value = '3600';
  document.getElementById('profileAddressList').value = '';
  document.getElementById('profilePrice').value = '150';
  openModal('profileModal');
}

function openEditProfileModal(p) {
  document.getElementById('profileModalTitle').innerText = `تعديل الباقة: ${p.name}`;
  document.getElementById('profileId').value = p.id;
  document.getElementById('profileName').value = p.name;
  document.getElementById('profileRateLimit').value = p.rate_limit;
  document.getElementById('profilePoolName').value = p.pool_name;
  document.getElementById('profileSessionTimeout').value = p.session_timeout;
  document.getElementById('profileIdleTimeout').value = p.idle_timeout;
  document.getElementById('profileAddressList').value = p.mikrotik_address_list;
  document.getElementById('profilePrice').value = p.price;
  openModal('profileModal');
}

async function saveProfileForm(e) {
  e.preventDefault();
  const id = parseInt(document.getElementById('profileId').value);
  const payload = {
    id: id,
    name: document.getElementById('profileName').value.trim(),
    rate_limit: document.getElementById('profileRateLimit').value.trim(),
    pool_name: document.getElementById('profilePoolName').value.trim(),
    session_timeout: parseInt(document.getElementById('profileSessionTimeout').value) || 0,
    idle_timeout: parseInt(document.getElementById('profileIdleTimeout').value) || 0,
    mikrotik_address_list: document.getElementById('profileAddressList').value.trim(),
    price: parseFloat(document.getElementById('profilePrice').value) || 0
  };

  try {
    const res = await fetch('/api/profiles', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    if (!res.ok) throw new Error('فشل الحفظ');
    closeModal('profileModal');
    loadProfiles();
  } catch (err) {
    alert(err.message);
  }
}

async function deleteProfile(id) {
  if (!confirm('هل أنت متأكد من حذف هذه الباقة؟')) return;
  try {
    await fetch(`/api/profiles?id=${id}`, { method: 'DELETE' });
    loadProfiles();
  } catch (err) {
    alert(err.message);
  }
}

// 6. NAS Management
async function loadNAS() {
  const tbody = document.getElementById('nasTableBody');
  try {
    const res = await fetch('/api/nas');
    const list = await res.json();
    if (!list || list.length === 0) {
      tbody.innerHTML = `<tr><td colspan="7" class="text-center text-muted">لا يوجد راوترات مايكروتك مضافة</td></tr>`;
      return;
    }

    tbody.innerHTML = list.map(n => `
      <tr>
        <td>${n.id}</td>
        <td><strong>${escapeHtml(n.name)}</strong></td>
        <td><code>${n.ip}</code></td>
        <td><code>${escapeHtml(n.secret)}</code></td>
        <td>${n.coa_port || 3799}</td>
        <td>${escapeHtml(n.description) || '-'}</td>
        <td>
          <button class="btn btn-sm btn-outline" onclick='openEditNASModal(${JSON.stringify(n)})'>تعديل</button>
          <button class="btn btn-sm btn-danger" onclick="deleteNAS(${n.id})">حذف</button>
        </td>
      </tr>
    `).join('');
  } catch (err) {
    console.error('Failed to load NAS', err);
  }
}

function openAddNASModal() {
  document.getElementById('nasModalTitle').innerText = 'إضافة راوتر مايكروتك';
  document.getElementById('nasId').value = '0';
  document.getElementById('nasName').value = '';
  document.getElementById('nasIP').value = '';
  document.getElementById('nasSecret').value = 'radius123';
  document.getElementById('nasCoAPort').value = '3799';
  document.getElementById('nasDesc').value = '';
  openModal('nasModal');
}

function openEditNASModal(n) {
  document.getElementById('nasModalTitle').innerText = `تعديل الراوتر: ${n.name}`;
  document.getElementById('nasId').value = n.id;
  document.getElementById('nasName').value = n.name;
  document.getElementById('nasIP').value = n.ip;
  document.getElementById('nasSecret').value = n.secret;
  document.getElementById('nasCoAPort').value = n.coa_port;
  document.getElementById('nasDesc').value = n.description;
  openModal('nasModal');
}

async function saveNASForm(e) {
  e.preventDefault();
  const id = parseInt(document.getElementById('nasId').value);
  const payload = {
    id: id,
    name: document.getElementById('nasName').value.trim(),
    ip: document.getElementById('nasIP').value.trim(),
    secret: document.getElementById('nasSecret').value.trim(),
    coa_port: parseInt(document.getElementById('nasCoAPort').value) || 3799,
    description: document.getElementById('nasDesc').value.trim()
  };

  try {
    const res = await fetch('/api/nas', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    if (!res.ok) throw new Error('فشل الحفظ');
    closeModal('nasModal');
    loadNAS();
    loadStats();
  } catch (err) {
    alert(err.message);
  }
}

async function deleteNAS(id) {
  if (!confirm('هل أنت متأكد من حذف راوتر المايكروتك؟')) return;
  try {
    await fetch(`/api/nas?id=${id}`, { method: 'DELETE' });
    loadNAS();
    loadStats();
  } catch (err) {
    alert(err.message);
  }
}

// 7. Accounting Logs
async function loadLogs(offset = 0) {
  const tbody = document.getElementById('logsTableBody');
  tbody.innerHTML = `<tr><td colspan="11" class="text-center">جاري جلب السجلات...</td></tr>`;

  try {
    const res = await fetch(`/api/logs?limit=50&offset=${offset}`);
    const data = await res.json();

    if (!data.logs || data.logs.length === 0) {
      tbody.innerHTML = `<tr><td colspan="11" class="text-center text-muted">لا يوجد سجلات حتى الآن</td></tr>`;
      renderPagination('logsPagination', 0, 50, 1, () => {});
      return;
    }

    tbody.innerHTML = data.logs.map(l => {
      const start = new Date(l.acctstarttime).toLocaleString('ar-EG');
      const stop = l.acctstoptime ? new Date(l.acctstoptime).toLocaleString('ar-EG') : '<span class="text-accent">متصل الآن</span>';
      return `
        <tr>
          <td>${l.radacctid}</td>
          <td><strong>${escapeHtml(l.username)}</strong></td>
          <td>${l.nasipaddress}</td>
          <td>${l.framedipaddress || '-'}</td>
          <td><code>${l.callingstationid || '-'}</code></td>
          <td>${start}</td>
          <td>${stop}</td>
          <td>${formatDuration(l.acctsessiontime)}</td>
          <td>${formatBytes(l.acctinputoctets)}</td>
          <td>${formatBytes(l.acctoutputoctets)}</td>
          <td>${escapeHtml(l.acctterminatecause) || '-'}</td>
        </tr>
      `;
    }).join('');

    const page = Math.floor(offset / 50) + 1;
    renderPagination('logsPagination', data.total, 50, page, (p) => loadLogs((p - 1) * 50));
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="11" class="text-center text-muted">خطأ: ${err.message}</td></tr>`;
  }
}

// 8. MikroTik Script
async function updateScript() {
  const ip = document.getElementById('scriptServerIP').value.trim() || window.location.hostname;
  const secret = document.getElementById('scriptSecret').value.trim() || 'radius123';
  try {
    const res = await fetch(`/api/mikrotik/script?serverIp=${ip}&secret=${encodeURIComponent(secret)}`);
    const txt = await res.text();
    document.getElementById('mikrotikScriptText').value = txt;
  } catch (err) {
    console.error(err);
  }
}

function copyMikrotikScript() {
  const box = document.getElementById('mikrotikScriptText');
  box.select();
  document.execCommand('copy');
  alert('تم نسخ كود إعداد المايكروتك بنجاح! الصقه في Terminal الراوتر.');
}

// Pagination helper
function renderPagination(containerId, total, pageSize, currentPage, onPageChange) {
  const container = document.getElementById(containerId);
  if (!container) return;

  const totalPages = Math.ceil(total / pageSize);
  if (totalPages <= 1) {
    container.innerHTML = `<span>إجمالي النتائج: ${total.toLocaleString()}</span>`;
    return;
  }

  container.innerHTML = `
    <span>صفحة ${currentPage} من ${totalPages} (إجمالي ${total.toLocaleString()})</span>
    <div style="display: flex; gap: 0.5rem;">
      <button class="btn btn-sm btn-outline" ${currentPage <= 1 ? 'disabled' : ''} onclick="changePage('${containerId}', ${currentPage - 1})">السابق</button>
      <button class="btn btn-sm btn-outline" ${currentPage >= totalPages ? 'disabled' : ''} onclick="changePage('${containerId}', ${currentPage + 1})">التالي</button>
    </div>
  `;
  container.pageCallback = onPageChange;
}

function changePage(containerId, page) {
  const container = document.getElementById(containerId);
  if (container && container.pageCallback) {
    container.pageCallback(page);
  }
}

// Modal Helpers
function openModal(id) {
  document.getElementById(id).classList.add('show');
}

function closeModal(id) {
  document.getElementById(id).classList.remove('show');
}

function escapeHtml(str) {
  if (!str) return '';
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}
