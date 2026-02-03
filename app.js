// GenX League Calendar - Team-focused Availability Tracker
// Any team member can manage their team

// ==================== STATE ====================

let currentUser = null;
let teams = [];
let weeks = [];
let userAvailability = {};
let myTeam = null;

// ==================== INIT ====================

document.addEventListener('DOMContentLoaded', init);

async function init() {
    setupTabs();
    startETClock();

    await checkAuth();
    await fetchTeams();
    await fetchWeeks();

    renderWeeksList();
    renderTeamRoster();

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
            const statusText = document.getElementById('syncStatusText');
            const sheetLink = document.getElementById('sheetLink');

            if (data.configured) {
                if (data.lastSync) {
                    const lastSync = new Date(data.lastSync);
                    statusText.innerHTML = `<span class="sync-ok">✓ Connected</span> Last sync: ${lastSync.toLocaleString()}`;
                } else {
                    statusText.innerHTML = '<span class="sync-ok">✓ Connected</span> Not synced yet';
                }
                if (data.sheetId) {
                    sheetLink.href = `https://docs.google.com/spreadsheets/d/${data.sheetId}`;
                    sheetLink.style.display = 'inline-block';
                }
            } else {
                statusText.innerHTML = '<span class="sync-warning">⚠ Not configured</span> Set GOOGLE_SHEETS_ID and GOOGLE_SERVICE_ACCOUNT_JSON';
            }
        }
    } catch (err) {
        console.error('Failed to load sync status:', err);
    }
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
    // Expected: Team Name, Player 1-5, Sub 1-4
    const teamsData = rows.map((row, index) => {
        const teamName = row[0];
        if (!teamName) return null;

        const players = [];
        for (let i = 1; i <= 5 && i < row.length; i++) {
            if (row[i]) players.push(row[i]);
        }

        const subs = [];
        for (let i = 6; i <= 9 && i < row.length; i++) {
            if (row[i]) subs.push(row[i]);
        }

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
