// GenX League Calendar - Team-focused Availability Tracker
// Any team member can manage their team

// ==================== STATE ====================

let currentUser = null;
let teams = [];
let weeks = [];
let subs = [];
let userAvailability = {};
let myTeam = null;
let mySub = null; // If current user is registered as sub

// ==================== INIT ====================

document.addEventListener('DOMContentLoaded', init);

async function init() {
    setupTabs();
    startETClock();

    await checkAuth();
    await fetchTeams();
    await fetchWeeks();
    await fetchSubs();
    await loadSubRules();
    await fetchRegisteredPlayers();

    renderWeeksList();
    renderTeamRoster();
    renderSubPool();

    // Handle Discord link with week parameter
    handleWeekLinkParam();
}

// ==================== AUTH ====================

async function checkAuth() {
    try {
        const res = await fetch('/auth/me', { credentials: 'include' });
        if (res.ok) {
            const data = await res.json();
            if (data.authenticated) {
                currentUser = {
                    discordId: data.discordId,
                    username: data.username,
                    displayName: data.displayName,
                    avatar: data.avatar,
                    isAdmin: data.isAdmin,
                    canManageTeam: data.canManageTeam,
                    teamName: data.teamName,
                    playerName: data.playerName
                };
                showLoggedIn();

                // Check if needs to link profile
                if (!currentUser.teamName) {
                    setTimeout(showLinkModal, 500);
                }
                return;
            }
        }
        showLoggedOut();
    } catch (err) {
        console.error('Auth check failed:', err);
        showLoggedOut();
    }
}

function showLoggedIn() {
    document.getElementById('loginSection').style.display = 'none';
    document.getElementById('userSection').style.display = 'flex';
    document.getElementById('userAvatar').src = currentUser.avatar || '';
    document.getElementById('userName').textContent = currentUser.displayName || currentUser.username;

    // Show admin tab if admin
    if (currentUser.isAdmin) {
        document.querySelectorAll('.admin-only').forEach(el => el.style.display = '');
        // Load admin users list
        loadAdminUsers();
    }

    // Show linked player info
    if (currentUser.teamName && currentUser.playerName) {
        document.getElementById('linkedInfo').textContent = `${currentUser.teamName} - ${currentUser.playerName}`;
        document.getElementById('noTeamWarning').style.display = 'none';

        // Find my team data
        myTeam = teams.find(t => t.name === currentUser.teamName);
    } else {
        document.getElementById('noTeamWarning').style.display = 'block';
    }

    // Show schedule content
    document.getElementById('loginRequired').style.display = 'none';
    document.getElementById('scheduleContent').style.display = 'block';

    // Load webhook if team member
    if (currentUser.canManageTeam) {
        loadWebhookSetting();
    }

    // Load admin data if admin
    if (currentUser.isAdmin) {
        renderAdminWeeksList();
    }
}

function showLoggedOut() {
    currentUser = null;
    document.getElementById('loginSection').style.display = 'block';
    document.getElementById('userSection').style.display = 'none';
    document.querySelectorAll('.admin-only').forEach(el => el.style.display = 'none');

    document.getElementById('loginRequired').style.display = 'block';
    document.getElementById('scheduleContent').style.display = 'none';
}

async function logout() {
    await fetch('/auth/logout', { method: 'POST', credentials: 'include' });
    currentUser = null;
    location.reload();
}

document.getElementById('logoutBtn')?.addEventListener('click', logout);

// ==================== DATA FETCHING ====================

async function fetchTeams() {
    try {
        const res = await fetch('/api/teams');
        if (res.ok) {
            teams = await res.json() || [];
        }
    } catch (err) {
        console.error('Failed to fetch teams:', err);
    }
}

async function fetchWeeks() {
    try {
        const res = await fetch('/api/weeks');
        if (res.ok) {
            weeks = await res.json() || [];
            // Also fetch user availability if logged in
            if (currentUser && currentUser.teamName) {
                await fetchUserAvailability();
            }
        }
    } catch (err) {
        console.error('Failed to fetch weeks:', err);
    }
}

async function fetchUserAvailability() {
    try {
        const res = await fetch('/api/availability', { credentials: 'include' });
        if (res.ok) {
            const data = await res.json() || [];
            userAvailability = {};
            data.forEach(a => {
                userAvailability[a.weekId] = a.available;
            });
        }
    } catch (err) {
        console.error('Failed to fetch availability:', err);
    }
}

async function fetchTeamWeekStatus(weekId) {
    try {
        const res = await fetch(`/api/team-availability/${weekId}`, { credentials: 'include' });
        if (res.ok) {
            return await res.json();
        }
    } catch (err) {
        console.error('Failed to fetch team status:', err);
    }
    return null;
}

// ==================== TEAM ROSTER ====================

function renderTeamRoster() {
    const container = document.getElementById('teamRoster');
    const header = document.getElementById('teamNameHeader');

    if (!currentUser || !currentUser.teamName) {
        header.textContent = 'My Team';
        container.innerHTML = '<p class="no-data">Link your profile to see your team roster.</p>';
        return;
    }

    const team = teams.find(t => t.name === currentUser.teamName);
    if (!team) {
        header.textContent = currentUser.teamName;
        container.innerHTML = '<p class="no-data">Team data not found.</p>';
        return;
    }

    header.textContent = team.name;
    myTeam = team;

    container.innerHTML = `
        <div class="roster-columns">
            <div class="roster-column">
                <h3>Main Roster</h3>
                <ul class="roster-list">
                    ${(team.players || []).map(p => `
                        <li class="${p === currentUser.playerName ? 'is-me' : ''}">${escapeHtml(p)}</li>
                    `).join('')}
                </ul>
            </div>
            <div class="roster-column">
                <h3>Substitutes</h3>
                <ul class="roster-list subs">
                    ${(team.subs || []).map(s => `
                        <li class="${s === currentUser.playerName ? 'is-me' : ''}">${escapeHtml(s)}</li>
                    `).join('')}
                </ul>
            </div>
        </div>
    `;
}

// ==================== WEEKS/SCHEDULE DISPLAY ====================

function renderWeeksList() {
    const container = document.getElementById('weeksList');
    if (!container) return;

    if (weeks.length === 0) {
        container.innerHTML = '<p class="no-data">No weeks scheduled yet. Check back soon!</p>';
        return;
    }

    // Filter to only show weeks relevant to user's team
    const userTeam = currentUser?.teamName;

    container.innerHTML = weeks.map(week => renderWeekCard(week, userTeam)).join('');
}

function renderWeekCard(week, userTeam) {
    const userLobby = userTeam ? findUserLobby(week, userTeam) : null;
    const availability = userAvailability[week.id];

    const dateStr = week.date ? formatDate(week.date) : 'TBD';
    const timeStr = week.time || '8:00 PM ET';

    // Get opponents if in a lobby
    let opponents = [];
    if (userLobby) {
        opponents = userLobby.teams.filter(t => t !== userTeam);
    }

    return `
        <div class="week-card ${availability !== undefined ? (availability ? 'available' : 'unavailable') : ''}" data-week-id="${week.id}">
            <div class="week-header">
                <h3>${escapeHtml(week.name)}</h3>
                <span class="week-date">${dateStr} @ ${timeStr}</span>
            </div>

            ${userLobby ? `
                <div class="week-lobby-info">
                    <span class="lobby-badge">${escapeHtml(userLobby.name)}</span>
                    <span class="opponent-count">${opponents.length} opponents</span>
                </div>
                <div class="opponents-list">
                    <strong>Playing against:</strong>
                    ${opponents.map(t => `<span class="opponent-tag">${escapeHtml(t)}</span>`).join('')}
                </div>
            ` : userTeam ? `
                <div class="week-lobby-info">
                    <span class="lobby-badge not-assigned">Lobby TBD</span>
                </div>
            ` : ''}

            ${currentUser && currentUser.teamName ? `
                <div class="availability-section">
                    <div class="availability-buttons">
                        <button class="btn ${availability === true ? 'btn-success active' : 'btn-outline'}"
                                onclick="setAvailability('${week.id}', true)">
                            ✓ I Can Play
                        </button>
                        <button class="btn ${availability === false ? 'btn-danger active' : 'btn-outline'}"
                                onclick="setAvailability('${week.id}', false)">
                            ✗ Can't Make It
                        </button>
                    </div>
                    ${availability !== undefined ? `
                        <p class="my-status">Your status: <strong>${availability ? '✓ Available' : '✗ Unavailable'}</strong></p>
                    ` : `
                        <p class="my-status pending">Please confirm your availability</p>
                    `}
                </div>

                <div class="week-actions">
                    <button class="btn btn-small btn-secondary" onclick="showTeamStatus('${week.id}')">
                        👥 Team Status
                    </button>
                    ${currentUser.canManageTeam ? `
                        <button class="btn btn-small btn-primary" onclick="announceWeek('${week.id}')">
                            📢 Announce to Team
                        </button>
                    ` : ''}
                </div>
            ` : ''}
        </div>
    `;
}

function findUserLobby(week, teamName) {
    if (!week.lobbies) return null;
    return week.lobbies.find(lobby => lobby.teams && lobby.teams.includes(teamName));
}

// ==================== AVAILABILITY ====================

async function setAvailability(weekId, available) {
    if (!currentUser || !currentUser.teamName) {
        alert('Please link your profile first');
        showLinkModal();
        return;
    }

    try {
        const res = await fetch('/api/availability', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ weekId, available })
        });

        if (res.ok) {
            userAvailability[weekId] = available;
            renderWeeksList();
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to update availability');
        }
    } catch (err) {
        console.error('Failed to set availability:', err);
        alert('Failed to update availability');
    }
}

// ==================== TEAM STATUS MODAL ====================

async function showTeamStatus(weekId) {
    const week = weeks.find(w => w.id === weekId);
    if (!week) return;

    const modal = document.getElementById('teamStatusModal');
    const title = document.getElementById('teamStatusTitle');
    const content = document.getElementById('teamStatusContent');

    title.textContent = `${currentUser.teamName} - ${week.name}`;
    content.innerHTML = '<p>Loading...</p>';
    modal.classList.add('active');

    const status = await fetchTeamWeekStatus(weekId);
    if (!status) {
        content.innerHTML = '<p>Failed to load team status.</p>';
        return;
    }

    const availableList = status.available || [];
    const unavailableList = status.unavailable || [];
    const notRespondedList = status.notResponded || [];
    const subsNeeded = status.subsNeeded || 0;

    content.innerHTML = `
        ${subsNeeded > 0 ? `
            <div class="subs-needed-alert">
                ⚠️ <strong>${subsNeeded} sub(s) needed!</strong>
            </div>
        ` : ''}

        <div class="status-columns">
            <div class="status-column available">
                <h4>✓ Available (${availableList.length})</h4>
                <ul>
                    ${availableList.length > 0 ?
                        availableList.map(p => `<li>${escapeHtml(p)}</li>`).join('') :
                        '<li class="none">No responses yet</li>'
                    }
                </ul>
            </div>

            <div class="status-column unavailable">
                <h4>✗ Unavailable (${unavailableList.length})</h4>
                <ul>
                    ${unavailableList.length > 0 ?
                        unavailableList.map(p => `<li>${escapeHtml(p)}</li>`).join('') :
                        '<li class="none">None</li>'
                    }
                </ul>
            </div>

            <div class="status-column pending">
                <h4>⏳ No Response (${notRespondedList.length})</h4>
                <ul>
                    ${notRespondedList.length > 0 ?
                        notRespondedList.map(p => `<li>${escapeHtml(p)}</li>`).join('') :
                        '<li class="none">Everyone responded!</li>'
                    }
                </ul>
            </div>
        </div>
    `;
}

function closeTeamStatusModal() {
    document.getElementById('teamStatusModal').classList.remove('active');
}

// ==================== LINK PROFILE ====================

function showLinkModal() {
    populateTeamSelect();
    document.getElementById('linkModal').classList.add('active');
}

function closeLinkModal() {
    document.getElementById('linkModal').classList.remove('active');
}

function populateTeamSelect() {
    const select = document.getElementById('linkTeamSelect');
    select.innerHTML = '<option value="">-- Select Team --</option>';
    teams.forEach(team => {
        select.innerHTML += `<option value="${escapeHtml(team.name)}">${escapeHtml(team.name)}</option>`;
    });

    // Pre-select if already linked
    if (currentUser?.teamName) {
        select.value = currentUser.teamName;
        updatePlayerSelect();
    }
}

function updatePlayerSelect() {
    const teamName = document.getElementById('linkTeamSelect').value;
    const select = document.getElementById('linkPlayerSelect');
    select.innerHTML = '<option value="">-- Select Player --</option>';

    const team = teams.find(t => t.name === teamName);
    if (!team) return;

    // Add main players
    (team.players || []).forEach(p => {
        select.innerHTML += `<option value="${escapeHtml(p)}">${escapeHtml(p)}</option>`;
    });

    // Add subs with indicator
    (team.subs || []).forEach(s => {
        select.innerHTML += `<option value="${escapeHtml(s)}">${escapeHtml(s)} (Sub)</option>`;
    });

    // Pre-select if already linked
    if (currentUser?.playerName && currentUser.teamName === teamName) {
        select.value = currentUser.playerName;
    }
}

async function linkPlayer() {
    const teamName = document.getElementById('linkTeamSelect').value;
    const playerName = document.getElementById('linkPlayerSelect').value;

    if (!teamName || !playerName) {
        alert('Please select both team and player');
        return;
    }

    try {
        const res = await fetch('/api/link-player', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ teamName, playerName })
        });

        if (res.ok) {
            currentUser.teamName = teamName;
            currentUser.playerName = playerName;
            currentUser.canManageTeam = true;
            closeLinkModal();
            showLoggedIn();
            await fetchUserAvailability();
            renderWeeksList();
            renderTeamRoster();
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to link profile');
        }
    } catch (err) {
        console.error('Failed to link player:', err);
        alert('Failed to link profile');
    }
}

// ==================== DISCORD LINK HANDLER ====================

function handleWeekLinkParam() {
    const params = new URLSearchParams(window.location.search);
    const weekId = params.get('week');

    // Also check localStorage for pending week (from pre-login)
    const pendingWeek = localStorage.getItem('pendingWeekId');

    const targetWeekId = weekId || pendingWeek;

    if (targetWeekId) {
        // Clear the pending week
        localStorage.removeItem('pendingWeekId');

        // Clean URL
        if (weekId) {
            window.history.replaceState({}, '', window.location.pathname);
        }

        // Show quick availability modal
        setTimeout(() => showQuickAvailabilityModal(targetWeekId), 500);
    }
}

function showQuickAvailabilityModal(weekId) {
    const week = weeks.find(w => w.id === weekId);
    if (!week) {
        console.error('Week not found:', weekId);
        return;
    }

    const modal = document.getElementById('quickAvailModal');
    const content = modal.querySelector('.modal-content');

    // Different content based on login state
    if (!currentUser) {
        content.innerHTML = `
            <h3>${escapeHtml(week.name)}</h3>
            <p>Login to confirm your availability.</p>
            <div class="modal-buttons">
                <button class="btn btn-discord" onclick="loginForWeek('${weekId}')">
                    <svg class="discord-icon" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M20.317 4.37a19.791 19.791 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 0 0 .031.057 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028 14.09 14.09 0 0 0 1.226-1.994.076.076 0 0 0-.041-.106 13.107 13.107 0 0 1-1.872-.892.077.077 0 0 1-.008-.128 10.2 10.2 0 0 0 .372-.292.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.299 12.299 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.839 19.839 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03z"/>
                    </svg>
                    Login with Discord
                </button>
                <button class="btn btn-secondary" onclick="closeQuickAvailModal()">Cancel</button>
            </div>
        `;
    } else if (!currentUser.teamName) {
        content.innerHTML = `
            <h3>${escapeHtml(week.name)}</h3>
            <p>Link your profile to confirm availability.</p>
            <div class="modal-buttons">
                <button class="btn btn-primary" onclick="closeQuickAvailModal(); showLinkModal();">Link Profile</button>
                <button class="btn btn-secondary" onclick="closeQuickAvailModal()">Cancel</button>
            </div>
        `;
    } else {
        const availability = userAvailability[weekId];
        const hasResponded = availability !== undefined;

        content.innerHTML = `
            <h3>${escapeHtml(week.name)}</h3>
            <p class="quick-avail-player">${escapeHtml(currentUser.teamName)}</p>
            <p class="quick-avail-name">${escapeHtml(currentUser.playerName)}</p>
            ${hasResponded ? `
                <p class="current-response">Current: <strong>${availability ? '✓ Available' : '✗ Unavailable'}</strong></p>
            ` : `
                <p>Can you play this week?</p>
            `}
            <div class="modal-buttons">
                <button class="btn ${availability === true ? 'btn-success active' : 'btn-success'}"
                        onclick="quickSetAvailability('${weekId}', true)">
                    ✓ I Can Play
                </button>
                <button class="btn ${availability === false ? 'btn-danger active' : 'btn-danger'}"
                        onclick="quickSetAvailability('${weekId}', false)">
                    ✗ Can't Make It
                </button>
            </div>
            <button class="btn btn-link" onclick="closeQuickAvailModal()">Close</button>
        `;
    }

    modal.classList.add('active');
}

function closeQuickAvailModal() {
    document.getElementById('quickAvailModal').classList.remove('active');
}

async function quickSetAvailability(weekId, available) {
    await setAvailability(weekId, available);
    closeQuickAvailModal();
}

function loginForWeek(weekId) {
    localStorage.setItem('pendingWeekId', weekId);
    window.location.href = '/auth/discord';
}

// ==================== TEAM WEBHOOK/ANNOUNCE ====================

async function loadWebhookSetting() {
    try {
        const res = await fetch('/api/webhook', { credentials: 'include' });
        if (res.ok) {
            const data = await res.json();
            document.getElementById('discordWebhook').value = data.webhookUrl || '';
        }
    } catch (err) {
        console.error('Failed to load webhook:', err);
    }
}

async function saveWebhook() {
    const webhookUrl = document.getElementById('discordWebhook').value.trim();

    try {
        const res = await fetch('/api/webhook', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ webhookUrl })
        });

        if (res.ok) {
            alert('Webhook saved!');
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to save webhook');
        }
    } catch (err) {
        console.error('Failed to save webhook:', err);
        alert('Failed to save webhook');
    }
}

async function announceWeek(weekId) {
    const week = weeks.find(w => w.id === weekId);
    if (!week) return;

    if (!confirm(`Send announcement for ${week.name} to your team's Discord?`)) {
        return;
    }

    try {
        const res = await fetch('/api/announce', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ weekId })
        });

        if (res.ok) {
            alert('Announcement sent to Discord!');
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to announce');
        }
    } catch (err) {
        console.error('Failed to announce:', err);
        alert('Failed to announce to Discord');
    }
}

// ==================== ADMIN FUNCTIONS ====================

async function loadSyncStatus() {
    try {
        const res = await fetch('/api/admin/sync-status', { credentials: 'include' });
        if (res.ok) {
            const data = await res.json();

            // Update last sync time
            const lastSyncEl = document.getElementById('lastSyncTime');
            if (lastSyncEl) {
                if (data.lastSync) {
                    const lastSync = new Date(data.lastSync);
                    lastSyncEl.textContent = lastSync.toLocaleString();
                } else {
                    lastSyncEl.textContent = 'Never';
                }
            }

            // Pre-fill URL if saved
            const urlInput = document.getElementById('sheetUrl');
            if (urlInput && data.publishedUrl) {
                urlInput.value = data.publishedUrl;
            }
        }
    } catch (err) {
        console.error('Failed to load sync status:', err);
    }
}

// Import from published URL
async function importFromURL() {
    const url = document.getElementById('sheetUrl').value.trim();
    if (!url) {
        alert('Please enter the published spreadsheet URL');
        return;
    }

    const statusDiv = document.getElementById('importStatus');
    statusDiv.innerHTML = '<span class="sync-pending">⏳ Importing from URL...</span>';
    statusDiv.className = 'import-status importing';

    try {
        // Try HTML import first (more reliable for published sheets)
        const res = await fetch('/api/admin/import-from-html', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ url })
        });

        if (res.ok) {
            const result = await res.json();
            statusDiv.innerHTML = `<span class="sync-ok">✓ Import successful!</span> ${result.teamsImported} teams, ${result.weeksImported} weeks imported`;
            statusDiv.className = 'import-status success';

            // Update last sync time
            document.getElementById('lastSyncTime').textContent = new Date().toLocaleString();

            // Refresh data
            await fetchTeams();
            await fetchWeeks();
            renderWeeksList();
            renderTeamRoster();
            renderAdminWeeksList();

            if (result.errors && result.errors.length > 0) {
                console.warn('Import warnings:', result.errors);
            }
        } else {
            const err = await res.json();
            statusDiv.innerHTML = `<span class="sync-error">✗ Import failed:</span> ${err.error}`;
            statusDiv.className = 'import-status error';
        }
    } catch (err) {
        console.error('Import error:', err);
        statusDiv.innerHTML = '<span class="sync-error">✗ Import failed</span>';
        statusDiv.className = 'import-status error';
    }
}

async function refreshFromURL() {
    const url = document.getElementById('sheetUrl').value.trim();
    if (!url) {
        // Try to load saved URL
        try {
            const res = await fetch('/api/admin/sync-status', { credentials: 'include' });
            if (res.ok) {
                const data = await res.json();
                if (data.publishedUrl) {
                    document.getElementById('sheetUrl').value = data.publishedUrl;
                    importFromURL();
                    return;
                }
            }
        } catch (err) {
            // ignore
        }
        alert('Please enter a spreadsheet URL first');
        return;
    }

    importFromURL();
}

async function triggerSync() {
    const statusText = document.getElementById('syncStatusText');
    statusText.innerHTML = '<span class="sync-pending">⏳ Syncing...</span>';

    try {
        const res = await fetch('/api/admin/sync', {
            method: 'POST',
            credentials: 'include'
        });

        if (res.ok) {
            const data = await res.json();
            statusText.innerHTML = `<span class="sync-ok">✓ Synced!</span> ${new Date(data.syncedAt).toLocaleString()}`;
            // Refresh data
            await fetchTeams();
            await fetchWeeks();
            renderWeeksList();
            renderTeamRoster();
            renderAdminWeeksList();
        } else {
            const err = await res.json();
            statusText.innerHTML = `<span class="sync-error">✗ Failed:</span> ${err.error}`;
        }
    } catch (err) {
        statusText.innerHTML = '<span class="sync-error">✗ Sync failed</span>';
    }
}

function renderAdminWeeksList() {
    const container = document.getElementById('adminWeeksList');
    if (!container) return;

    // Load sync status when admin tab is rendered
    loadSyncStatus();

    if (weeks.length === 0) {
        container.innerHTML = '<p class="no-data">No weeks imported yet. Sync from Google Sheets or import manually.</p>';
        return;
    }

    container.innerHTML = weeks.map(week => `
        <div class="admin-week-item">
            <div class="admin-week-info">
                <span class="admin-week-name">${escapeHtml(week.name)}</span>
                <span class="admin-week-date">${week.date ? formatDate(week.date) : 'No date'}</span>
                <span class="admin-week-lobbies">${(week.lobbies || []).length} lobbies</span>
            </div>
        </div>
    `).join('');
}

// Import functions
function showImportTeamsModal() {
    document.getElementById('importTeamsModal').classList.add('active');
}

function closeImportTeamsModal() {
    document.getElementById('importTeamsModal').classList.remove('active');
}

function showImportWeeksModal() {
    document.getElementById('importWeeksModal').classList.add('active');
}

function closeImportWeeksModal() {
    document.getElementById('importWeeksModal').classList.remove('active');
}

// Parse pasted spreadsheet data (tab or comma separated)
function parseSpreadsheetData(text) {
    const lines = text.trim().split('\n');
    return lines.map(line => {
        // Try tab-separated first, then comma
        if (line.includes('\t')) {
            return line.split('\t').map(cell => cell.trim());
        } else {
            return line.split(',').map(cell => cell.trim());
        }
    }).filter(row => row.length > 0 && row[0] !== '');
}

async function importTeamsFromPaste() {
    const text = document.getElementById('teamsData').value.trim();
    if (!text) {
        alert('Please paste the teams data');
        return;
    }

    const rows = parseSpreadsheetData(text);
    if (rows.length === 0) {
        alert('No data found');
        return;
    }

    // Convert to teams format
    // Format: Team Name, Player 1, (empty), Player 2, Player 3, Player 4, Player 5, Sub 1, Sub 2, ...
    // Note: There's an empty column between Player 1 and Player 2
    const teamsData = rows.map((row, index) => {
        const teamName = row[0];
        if (!teamName) return null;

        // Get all non-empty values after team name
        const allMembers = [];
        for (let i = 1; i < row.length; i++) {
            if (row[i] && row[i].trim()) {
                allMembers.push(row[i].trim());
            }
        }

        // First 5 are players, rest are subs
        const players = allMembers.slice(0, 5);
        const subs = allMembers.slice(5);

        return {
            name: teamName,
            players: players,
            subs: subs
        };
    }).filter(t => t !== null);

    if (teamsData.length === 0) {
        alert('Could not parse any teams from the data');
        return;
    }

    try {
        const res = await fetch('/api/admin/import-teams', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify(teamsData)
        });

        if (res.ok) {
            const result = await res.json();
            alert(`Imported ${result.count} teams!`);
            closeImportTeamsModal();
            await fetchTeams();
            renderTeamRoster();
            renderWeeksList();
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to import');
        }
    } catch (err) {
        console.error('Import error:', err);
        alert('Failed to import teams');
    }
}

async function importScheduleFromPaste() {
    const text = document.getElementById('scheduleData').value.trim();
    if (!text) {
        alert('Please paste the schedule data');
        return;
    }

    const rows = parseSpreadsheetData(text);
    if (rows.length === 0) {
        alert('No data found');
        return;
    }

    // Group by week
    // Expected: Week, Lobby, Team 1, Team 2, ...
    const weekMap = new Map();

    rows.forEach(row => {
        const weekName = row[0];
        const lobbyName = row[1];
        if (!weekName || !lobbyName) return;

        const teams = [];
        for (let i = 2; i < row.length; i++) {
            if (row[i]) teams.push(row[i]);
        }

        if (!weekMap.has(weekName)) {
            weekMap.set(weekName, {
                id: weekName.toLowerCase().replace(/\s+/g, '-'),
                name: weekName,
                number: weekMap.size + 1,
                lobbies: []
            });
        }

        weekMap.get(weekName).lobbies.push({
            name: lobbyName,
            teams: teams
        });
    });

    const weeksData = Array.from(weekMap.values());

    if (weeksData.length === 0) {
        alert('Could not parse any weeks from the data');
        return;
    }

    try {
        const res = await fetch('/api/admin/import-weeks', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify(weeksData)
        });

        if (res.ok) {
            const result = await res.json();
            alert(`Imported ${result.count} weeks!`);
            closeImportWeeksModal();
            await fetchWeeks();
            renderWeeksList();
            renderAdminWeeksList();
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to import');
        }
    } catch (err) {
        console.error('Import error:', err);
        alert('Failed to import schedule');
    }
}

// ==================== SUB POOL ====================

async function fetchSubs() {
    try {
        const res = await fetch('/api/subs', { credentials: 'include' });
        if (res.ok) {
            subs = await res.json();
            // Check if current user is registered as sub
            if (currentUser) {
                mySub = subs.find(s => s.discordId === currentUser.discordId);
            }
        }
    } catch (err) {
        console.error('Failed to fetch subs:', err);
        subs = [];
    }
}

function renderSubPool() {
    const container = document.getElementById('subPoolList');
    if (!container) return;

    // Update action buttons based on login state
    updateSubPoolActions();

    // Get filter settings
    const sortBy = document.getElementById('subSortBy')?.value || 'compRank';
    const availableOnly = document.getElementById('showAvailableOnly')?.checked ?? true;

    // Filter and sort subs
    let filteredSubs = [...subs];
    if (availableOnly) {
        filteredSubs = filteredSubs.filter(s => s.available);
    }

    filteredSubs.sort((a, b) => {
        if (sortBy === 'compRank') return (b.compRank || 0) - (a.compRank || 0);
        if (sortBy === 'name') return a.name.localeCompare(b.name);
        return 0;
    });

    if (filteredSubs.length === 0) {
        container.innerHTML = '<p class="no-data">No subs available. Be the first to register!</p>';
        return;
    }

    container.innerHTML = filteredSubs.map(sub => `
        <div class="sub-card ${sub.available ? 'available' : 'unavailable'} ${mySub?.id === sub.id ? 'is-me' : ''}">
            <div class="sub-info">
                <span class="sub-name">${escapeHtml(sub.name)}</span>
                ${sub.discordName ? `<span class="sub-discord">@${escapeHtml(sub.discordName)}</span>` : ''}
            </div>
            <div class="sub-ranks">
                <span class="rank comp-rank" title="Comp Rank">CR: ${sub.compRank || '?'}</span>
            </div>
            <div class="sub-status">
                <span class="status-badge ${sub.available ? 'available' : 'unavailable'}">
                    ${sub.available ? '✓ Available' : '✗ Unavailable'}
                </span>
            </div>
            ${sub.notes ? `<div class="sub-notes">${escapeHtml(sub.notes)}</div>` : ''}
        </div>
    `).join('');
}

function updateSubPoolActions() {
    const registerBtn = document.getElementById('registerSubBtn');
    const unregisterBtn = document.getElementById('unregisterSubBtn');
    const toggleBtn = document.getElementById('toggleSubAvailBtn');

    if (!currentUser) {
        // Not logged in - hide all buttons
        if (registerBtn) registerBtn.style.display = 'none';
        if (unregisterBtn) unregisterBtn.style.display = 'none';
        if (toggleBtn) toggleBtn.style.display = 'none';
        return;
    }

    if (mySub) {
        // Already registered - show unregister and toggle
        if (registerBtn) registerBtn.style.display = 'none';
        if (unregisterBtn) unregisterBtn.style.display = 'inline-block';
        if (toggleBtn) {
            toggleBtn.style.display = 'inline-block';
            toggleBtn.textContent = mySub.available ? 'Mark Unavailable' : 'Mark Available';
        }
    } else {
        // Not registered - show register button
        if (registerBtn) registerBtn.style.display = 'inline-block';
        if (unregisterBtn) unregisterBtn.style.display = 'none';
        if (toggleBtn) toggleBtn.style.display = 'none';
    }
}

let currentSubCR = null; // Stores the looked-up CR for registration

async function showRegisterSubModal() {
    const nameInput = document.getElementById('subName');
    const crDisplay = document.getElementById('subCRDisplay');
    const submitBtn = document.getElementById('registerSubSubmitBtn');
    const errorDiv = document.getElementById('subRegisterError');

    // Reset state
    nameInput.value = '';
    currentSubCR = null;
    submitBtn.disabled = true;
    errorDiv.style.display = 'none';
    updateCRDisplay(null, 'Enter your name to lookup CR');

    // Pre-fill with user info if available
    if (currentUser?.playerName) {
        nameInput.value = currentUser.playerName;
        await lookupAndDisplayCR(currentUser.playerName);
    }

    // Add event listener for name changes to auto-lookup CR
    nameInput.oninput = debounce(async function() {
        const name = this.value.trim();
        if (name) {
            await lookupAndDisplayCR(name);
        } else {
            currentSubCR = null;
            submitBtn.disabled = true;
            updateCRDisplay(null, 'Enter your name to lookup CR');
        }
    }, 500);

    document.getElementById('registerSubModal').classList.add('active');
}

function updateCRDisplay(cr, status) {
    const crDisplay = document.getElementById('subCRDisplay');
    if (!crDisplay) return;

    const valueEl = crDisplay.querySelector('.cr-value');
    const statusEl = crDisplay.querySelector('.cr-status');

    if (cr !== null) {
        valueEl.textContent = cr;
        valueEl.classList.add('found');
        statusEl.textContent = status || 'Found in registered players';
        statusEl.classList.remove('error');
        statusEl.classList.add('success');
    } else {
        valueEl.textContent = '--';
        valueEl.classList.remove('found');
        statusEl.textContent = status || 'Not found';
        statusEl.classList.remove('success');
        if (status && status.includes('not found')) {
            statusEl.classList.add('error');
        } else {
            statusEl.classList.remove('error');
        }
    }
}

async function lookupAndDisplayCR(name) {
    const submitBtn = document.getElementById('registerSubSubmitBtn');
    const errorDiv = document.getElementById('subRegisterError');

    updateCRDisplay(null, 'Looking up...');

    const cr = await lookupPlayerCR(name);

    if (cr !== null) {
        currentSubCR = cr;
        submitBtn.disabled = false;
        errorDiv.style.display = 'none';
        updateCRDisplay(cr, 'Found in registered players');
    } else {
        currentSubCR = null;
        submitBtn.disabled = true;
        updateCRDisplay(null, `"${name}" not found in registered players`);
        errorDiv.innerHTML = 'Your name must be registered via stat-bot before you can join the sub pool. Contact a league admin if you believe this is an error.';
        errorDiv.style.display = 'block';
    }
}

// Debounce helper to avoid too many API calls
function debounce(func, wait) {
    let timeout;
    return function executedFunction(...args) {
        const later = () => {
            clearTimeout(timeout);
            func.apply(this, args);
        };
        clearTimeout(timeout);
        timeout = setTimeout(later, wait);
    };
}

function closeRegisterSubModal() {
    document.getElementById('registerSubModal').classList.remove('active');
}

async function registerAsSub() {
    const name = document.getElementById('subName').value.trim();
    const notes = document.getElementById('subNotes').value.trim();

    if (!name) {
        alert('Please enter your in-game name');
        return;
    }

    if (currentSubCR === null) {
        alert('Your name must be found in the registered players list');
        return;
    }

    try {
        const res = await fetch('/api/subs/register', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ name, notes })
        });

        if (res.ok) {
            const data = await res.json();
            mySub = data.sub;
            closeRegisterSubModal();
            await fetchSubs();
            renderSubPool();
            alert('You are now registered as a sub!');
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to register');
        }
    } catch (err) {
        console.error('Register error:', err);
        alert('Failed to register as sub');
    }
}

async function unregisterAsSub() {
    if (!confirm('Remove yourself from the sub pool?')) return;

    try {
        const res = await fetch('/api/subs/unregister', {
            method: 'DELETE',
            credentials: 'include'
        });

        if (res.ok) {
            mySub = null;
            await fetchSubs();
            renderSubPool();
            alert('You have been removed from the sub pool.');
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to unregister');
        }
    } catch (err) {
        console.error('Unregister error:', err);
        alert('Failed to unregister');
    }
}

async function toggleSubAvailability() {
    if (!mySub) return;

    try {
        const res = await fetch('/api/subs/availability', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ available: !mySub.available })
        });

        if (res.ok) {
            mySub.available = !mySub.available;
            await fetchSubs();
            renderSubPool();
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to update availability');
        }
    } catch (err) {
        console.error('Toggle error:', err);
        alert('Failed to update availability');
    }
}

// Admin: Import subs
function showImportSubsModal() {
    document.getElementById('importSubsModal').classList.add('active');
}

function closeImportSubsModal() {
    document.getElementById('importSubsModal').classList.remove('active');
}

async function importSubsFromPaste() {
    const text = document.getElementById('subsData').value.trim();
    if (!text) {
        alert('Please paste the subs data');
        return;
    }

    try {
        const res = await fetch('/api/admin/import-subs', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ data: text })
        });

        if (res.ok) {
            const result = await res.json();
            alert(`Imported ${result.imported} subs!`);
            closeImportSubsModal();
            await fetchSubs();
            renderSubPool();
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to import');
        }
    } catch (err) {
        console.error('Import error:', err);
        alert('Failed to import subs');
    }
}

// ==================== REGISTERED PLAYERS ====================

async function fetchRegisteredPlayers() {
    try {
        const res = await fetch('/api/players', { credentials: 'include' });
        if (res.ok) {
            registeredPlayers = await res.json();
            updateRegisteredPlayersCount();
        }
    } catch (err) {
        console.error('Failed to fetch registered players:', err);
        registeredPlayers = [];
    }
}

function updateRegisteredPlayersCount() {
    const countEl = document.getElementById('registeredPlayersCount');
    if (countEl) {
        countEl.textContent = registeredPlayers.length;
    }
}

function showImportPlayersModal() {
    document.getElementById('importPlayersModal').classList.add('active');
}

function closeImportPlayersModal() {
    document.getElementById('importPlayersModal').classList.remove('active');
}

async function importPlayersFromPaste() {
    const text = document.getElementById('playersData').value.trim();
    if (!text) {
        alert('Please paste the players data');
        return;
    }

    const replace = document.getElementById('replacePlayersOnImport').checked;

    try {
        const res = await fetch('/api/admin/import-players', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ data: text, replace })
        });

        if (res.ok) {
            const result = await res.json();
            alert(`Imported ${result.imported} registered players!`);
            closeImportPlayersModal();
            await fetchRegisteredPlayers();
            renderRegisteredPlayersList();
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to import');
        }
    } catch (err) {
        console.error('Import error:', err);
        alert('Failed to import registered players');
    }
}

function toggleRegisteredPlayersList() {
    const list = document.getElementById('registeredPlayersList');
    if (list.style.display === 'none') {
        list.style.display = 'block';
        renderRegisteredPlayersList();
    } else {
        list.style.display = 'none';
    }
}

function renderRegisteredPlayersList() {
    const container = document.getElementById('registeredPlayersList');
    if (!container) return;

    if (registeredPlayers.length === 0) {
        container.innerHTML = '<p class="no-data">No registered players. Import from stat-bot to populate.</p>';
        return;
    }

    // Sort by CR descending
    const sorted = [...registeredPlayers].sort((a, b) => (b.compRank || 0) - (a.compRank || 0));

    container.innerHTML = `
        <div class="players-table">
            <div class="players-header">
                <span class="player-col-name">Name</span>
                <span class="player-col-cr">CR</span>
            </div>
            ${sorted.map(p => `
                <div class="player-row">
                    <span class="player-col-name">${escapeHtml(p.name)}</span>
                    <span class="player-col-cr">${p.compRank || '?'}</span>
                </div>
            `).join('')}
        </div>
    `;
}

// Lookup player CR when registering as sub
async function lookupPlayerCR(name) {
    try {
        const res = await fetch(`/api/players/lookup?name=${encodeURIComponent(name)}`, { credentials: 'include' });
        if (res.ok) {
            const data = await res.json();
            if (data.found) {
                return data.compRank;
            }
        }
    } catch (err) {
        console.error('Failed to lookup player CR:', err);
    }
    return null;
}

// Sub matching rules
let subRules = { tiers: [], maxOverage: 0, equalFloor: 0 };
let eligibleFilter = null; // When set, only show eligible subs
let registeredPlayers = []; // All registered players from stat-bot

async function loadSubRules() {
    try {
        const res = await fetch('/api/admin/sub-rules', { credentials: 'include' });
        if (res.ok) {
            subRules = await res.json();
            // Update form fields
            const maxOverageInput = document.getElementById('maxOverage');
            const equalFloorInput = document.getElementById('equalFloor');
            if (maxOverageInput) maxOverageInput.value = subRules.maxOverage || 0;
            if (equalFloorInput) equalFloorInput.value = subRules.equalFloor || 0;
            renderTiersList();
        }
    } catch (err) {
        console.error('Failed to load sub rules:', err);
    }
}

function renderTiersList() {
    const container = document.getElementById('tiersList');
    if (!container) return;

    const tiers = subRules.tiers || [];

    if (tiers.length === 0) {
        container.innerHTML = '<p class="no-data">No tiers configured. Without tiers, subs must have CR ≤ outgoing player\'s CR.</p>';
        return;
    }

    container.innerHTML = tiers.map((tier, index) => `
        <div class="tier-item" data-tier-id="${tier.id}">
            <div class="tier-info">
                <span class="tier-name">${escapeHtml(tier.name)}</span>
                <span class="tier-range">CR ${tier.min} - ${tier.max}</span>
            </div>
            <button class="btn btn-small btn-danger" onclick="removeTier('${tier.id}')">Remove</button>
        </div>
    `).join('');
}

function addTier() {
    const nameInput = document.getElementById('newTierName');
    const minInput = document.getElementById('newTierMin');
    const maxInput = document.getElementById('newTierMax');

    const name = nameInput.value.trim();
    const min = parseInt(minInput.value) || 0;
    const max = parseInt(maxInput.value) || 0;

    if (!name) {
        alert('Please enter a tier name');
        return;
    }
    if (max < min) {
        alert('Max must be greater than or equal to Min');
        return;
    }

    const tiers = subRules.tiers || [];
    const newTier = {
        id: 'tier-' + Date.now(),
        name: name,
        min: min,
        max: max
    };

    tiers.push(newTier);
    subRules.tiers = tiers;

    // Clear inputs
    nameInput.value = '';
    minInput.value = '';
    maxInput.value = '';

    renderTiersList();
}

function removeTier(tierId) {
    subRules.tiers = (subRules.tiers || []).filter(t => t.id !== tierId);
    renderTiersList();
}

async function saveSubRules() {
    const tiers = subRules.tiers || [];
    const maxOverage = parseInt(document.getElementById('maxOverage')?.value) || 0;
    const equalFloor = parseInt(document.getElementById('equalFloor')?.value) || 0;

    try {
        const res = await fetch('/api/admin/sub-rules', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ tiers, maxOverage, equalFloor })
        });

        const statusEl = document.getElementById('subRulesStatus');
        if (res.ok) {
            subRules.maxOverage = maxOverage;
            subRules.equalFloor = equalFloor;
            if (statusEl) statusEl.textContent = '✓ Rules saved!';
        } else {
            const err = await res.json();
            if (statusEl) statusEl.textContent = '✗ ' + (err.error || 'Failed to save');
        }
    } catch (err) {
        console.error('Save error:', err);
        const statusEl = document.getElementById('subRulesStatus');
        if (statusEl) statusEl.textContent = '✗ Failed to save rules';
    }
}

async function findEligibleSubs() {
    const outgoingCR = parseInt(document.getElementById('outgoingCR').value);
    if (!outgoingCR && outgoingCR !== 0) {
        alert('Please enter the outgoing player\'s Comp Rank');
        return;
    }

    try {
        const res = await fetch(`/api/subs/eligible?cr=${outgoingCR}`, { credentials: 'include' });
        if (res.ok) {
            const data = await res.json();
            eligibleFilter = {
                outgoingCR: data.outgoingCR,
                eligibleIds: data.eligible.map(s => s.id)
            };

            const statusEl = document.getElementById('eligibleStatus');
            if (statusEl) {
                let statusText = `Found ${data.eligible.length} eligible subs for CR ${outgoingCR}`;
                if (data.reason) {
                    statusText += ` — ${data.reason}`;
                }
                statusEl.textContent = statusText;
            }

            renderSubPool();
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to find eligible subs');
        }
    } catch (err) {
        console.error('Eligible subs error:', err);
        alert('Failed to find eligible subs');
    }
}

function clearEligibleFilter() {
    eligibleFilter = null;
    document.getElementById('outgoingCR').value = '';
    const statusEl = document.getElementById('eligibleStatus');
    if (statusEl) statusEl.textContent = '';
    renderSubPool();
}

// Update renderSubPool to handle eligible filter
const originalRenderSubPool = renderSubPool;
renderSubPool = function() {
    const container = document.getElementById('subPoolList');
    if (!container) return;

    // Update action buttons based on login state
    updateSubPoolActions();

    // Get filter settings
    const sortBy = document.getElementById('subSortBy')?.value || 'compRank';
    const availableOnly = document.getElementById('showAvailableOnly')?.checked ?? true;

    // Filter and sort subs
    let filteredSubs = [...subs];

    if (availableOnly) {
        filteredSubs = filteredSubs.filter(s => s.available);
    }

    // Apply eligible filter if set
    if (eligibleFilter) {
        filteredSubs = filteredSubs.filter(s => eligibleFilter.eligibleIds.includes(s.id));
    }

    filteredSubs.sort((a, b) => {
        if (sortBy === 'compRank') return (b.compRank || 0) - (a.compRank || 0);
        if (sortBy === 'name') return a.name.localeCompare(b.name);
        return 0;
    });

    if (filteredSubs.length === 0) {
        if (eligibleFilter) {
            container.innerHTML = '<p class="no-data">No eligible subs found for this CR. Try a higher rank or check if subs are available.</p>';
        } else {
            container.innerHTML = '<p class="no-data">No subs available. Be the first to register!</p>';
        }
        return;
    }

    container.innerHTML = filteredSubs.map(sub => `
        <div class="sub-card ${sub.available ? 'available' : 'unavailable'} ${mySub?.id === sub.id ? 'is-me' : ''} ${eligibleFilter ? 'eligible' : ''}">
            <div class="sub-info">
                <span class="sub-name">${escapeHtml(sub.name)}</span>
                ${sub.discordName ? `<span class="sub-discord">@${escapeHtml(sub.discordName)}</span>` : ''}
            </div>
            <div class="sub-ranks">
                <span class="rank comp-rank" title="Comp Rank">CR: ${sub.compRank || '?'}</span>
            </div>
            <div class="sub-status">
                <span class="status-badge ${sub.available ? 'available' : 'unavailable'}">
                    ${sub.available ? '✓ Available' : '✗ Unavailable'}
                </span>
            </div>
            ${sub.notes ? `<div class="sub-notes">${escapeHtml(sub.notes)}</div>` : ''}
        </div>
    `).join('');
};

// ==================== TABS ====================

function setupTabs() {
    document.querySelectorAll('.tab').forEach(tab => {
        tab.addEventListener('click', () => {
            const targetId = tab.dataset.tab;

            document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            tab.classList.add('active');

            document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
            document.getElementById(targetId)?.classList.add('active');
        });
    });
}

// ==================== ET CLOCK ====================

function startETClock() {
    updateETClock();
    setInterval(updateETClock, 1000);
}

function updateETClock() {
    const now = new Date();
    const etTime = now.toLocaleTimeString('en-US', {
        timeZone: 'America/New_York',
        hour: '2-digit',
        minute: '2-digit',
        hour12: true
    });
    const clockEl = document.getElementById('etClock');
    if (clockEl) clockEl.textContent = etTime;
}

// ==================== UTILITIES ====================

function escapeHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

function formatDate(dateStr) {
    if (!dateStr) return '';
    const date = new Date(dateStr + 'T00:00:00');
    return date.toLocaleDateString('en-US', {
        weekday: 'short',
        month: 'short',
        day: 'numeric'
    });
}

// ==================== ADMIN USER MANAGEMENT ====================

let adminUsers = [];

async function loadAdminUsers() {
    const container = document.getElementById('adminUsersList');
    if (!container) return;

    container.innerHTML = '<p class="loading">Loading users...</p>';

    try {
        const res = await fetch('/api/admin/users', { credentials: 'include' });
        if (res.ok) {
            adminUsers = await res.json();
            renderAdminUsersList();
        } else {
            container.innerHTML = '<p class="error">Failed to load users</p>';
        }
    } catch (err) {
        console.error('Failed to load users:', err);
        container.innerHTML = '<p class="error">Failed to load users</p>';
    }
}

function renderAdminUsersList() {
    const container = document.getElementById('adminUsersList');
    if (!container) return;

    if (adminUsers.length === 0) {
        container.innerHTML = '<p class="no-data">No users have logged in yet.</p>';
        return;
    }

    // Sort: admins first, then by name
    const sorted = [...adminUsers].sort((a, b) => {
        if (a.isAdmin !== b.isAdmin) return b.isAdmin ? 1 : -1;
        return (a.displayName || a.username).localeCompare(b.displayName || b.username);
    });

    container.innerHTML = `
        <div class="users-table">
            <div class="users-header">
                <span class="user-col-avatar"></span>
                <span class="user-col-name">User</span>
                <span class="user-col-team">Team / Player</span>
                <span class="user-col-admin">Admin</span>
            </div>
            ${sorted.map(user => `
                <div class="user-row ${user.isAdmin ? 'is-admin' : ''} ${user.discordId === currentUser?.discordId ? 'is-me' : ''}">
                    <span class="user-col-avatar">
                        ${user.avatar ? `<img src="${user.avatar}" class="user-mini-avatar" alt="">` : ''}
                    </span>
                    <span class="user-col-name">
                        <span class="user-display-name">${escapeHtml(user.displayName || user.username)}</span>
                        <span class="user-username">@${escapeHtml(user.username)}</span>
                    </span>
                    <span class="user-col-team">
                        ${user.teamName ? `${escapeHtml(user.teamName)}` : '<span class="not-linked">Not linked</span>'}
                        ${user.playerName ? `<br><small>${escapeHtml(user.playerName)}</small>` : ''}
                    </span>
                    <span class="user-col-admin">
                        ${user.discordId === currentUser?.discordId ?
                            '<span class="admin-badge-you">You</span>' :
                            `<button class="btn btn-small ${user.isAdmin ? 'btn-danger' : 'btn-success'}"
                                     onclick="toggleUserAdmin('${user.discordId}', ${!user.isAdmin})">
                                ${user.isAdmin ? 'Remove Admin' : 'Make Admin'}
                            </button>`
                        }
                    </span>
                </div>
            `).join('')}
        </div>
    `;
}

async function toggleUserAdmin(discordId, makeAdmin) {
    const user = adminUsers.find(u => u.discordId === discordId);
    if (!user) return;

    const action = makeAdmin ? 'grant admin access to' : 'remove admin access from';
    if (!confirm(`Are you sure you want to ${action} ${user.displayName || user.username}?`)) {
        return;
    }

    try {
        const res = await fetch('/api/admin/set-admin', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ discordId, isAdmin: makeAdmin })
        });

        if (res.ok) {
            const result = await res.json();
            // Update local state
            const userIndex = adminUsers.findIndex(u => u.discordId === discordId);
            if (userIndex !== -1) {
                adminUsers[userIndex].isAdmin = result.isAdmin;
            }
            renderAdminUsersList();
            alert(`${result.displayName} is ${result.isAdmin ? 'now an admin' : 'no longer an admin'}`);
        } else {
            const err = await res.json();
            alert(err.error || 'Failed to update admin status');
        }
    } catch (err) {
        console.error('Failed to update admin:', err);
        alert('Failed to update admin status');
    }
}
