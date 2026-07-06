document.addEventListener('DOMContentLoaded', () => {
    // ---- apiFetch wrapper (checks 401/403) ----
    async function apiFetch(url, options = {}) {
        const res = await fetch(url, options);
        if (res.status === 401 || res.status === 403) {
            window.location.href = '/login';
            throw new Error('Session expired');
        }
        return res;
    }

    // ---- Interval Tracking & Cleanup ----
    const _intervalIds = [];
    function trackedSetInterval(fn, ms) {
        const id = setInterval(fn, ms);
        _intervalIds.push(id);
        return id;
    }

    window.addEventListener('beforeunload', () => {
        _intervalIds.forEach(id => clearInterval(id));
    });

    // ---- Global Elements ----
    const statusDot = document.getElementById('status-dot');
    const statusText = document.getElementById('status-text');
    
    // ---- Page Specific Elements ----
    
    // Dashboard
    const metricSent = document.getElementById('metric-sent');
    const metricScheduled = document.getElementById('metric-scheduled');
    const metricFailed = document.getElementById('metric-failed');

    // Dashboard connection card
    const connIndicator = document.getElementById('conn-indicator');
    const connTitle = document.getElementById('conn-title');
    const connDesc = document.getElementById('conn-desc');
    const connBadge = document.getElementById('conn-badge');

    // Single Send
    const sendMessageForm = document.getElementById('sendMessageForm');
    const singleFeedback = document.getElementById('singleFeedback');
    const sendBtn = document.getElementById('sendBtn');
    
    // Bulk Send
    const bulkForm = document.getElementById('bulkForm');
    const bulkFeedback = document.getElementById('bulkFeedback');
    const bulkBtn = document.getElementById('bulkBtn');

    // Schedule
    const scheduleForm = document.getElementById('scheduleForm');
    const schFeedback = document.getElementById('schFeedback');
    const schBtn = document.getElementById('schBtn');

    // Connection Management
    const loggedInView = document.getElementById('logged-in-view');
    const loggedOutView = document.getElementById('logged-out-view');
    const disconnectedView = document.getElementById('disconnected-view');
    const qrDisplay = document.getElementById('qr-display');
    const qrLoading = document.getElementById('qr-loading');
    const qrError = document.getElementById('qr-error');
    const qrImage = document.getElementById('qr-image');
    
    const disconnectBtn = document.getElementById('disconnectBtn');
    const logoutBtn = document.getElementById('logoutBtn');
    const connectBtn = document.getElementById('connectBtn');
    const retryQRBtn = document.getElementById('retryQRBtn');
    const connFeedback = document.getElementById('connFeedback');

    let isConnected = false;
    let isLoggedIn = false;
    let qrPollInterval = null;

    // ---- Status & Metrics Polling ----
    async function checkStatus() {
        try {
            const res = await apiFetch('/api/status');
            if (!res.ok) throw new Error(`Status endpoint returned ${res.status}`);
            const data = await res.json();
            
            isConnected = data.connected;
            isLoggedIn = data.loggedIn;
            
            if (statusDot && statusText) {
                // Reset classes for sidebar status dot
                statusDot.className = 'status-dot';
                
                if (isConnected && isLoggedIn) {
                    statusDot.classList.add('connected');
                    statusText.textContent = 'Connected';
                } else if (isConnected && !isLoggedIn) {
                    statusDot.classList.add('awaiting', 'pulsing');
                    statusText.textContent = 'Awaiting Scan';
                } else if (!isConnected && isLoggedIn) {
                    statusDot.classList.add('disconnected', 'pulsing');
                    statusText.textContent = 'Disconnected';
                } else {
                    statusDot.classList.add('offline', 'pulsing');
                    statusText.textContent = 'Logged Out';
                }
            }

            // Update dashboard connection card
            if (connIndicator && connBadge) {
                if (isConnected && isLoggedIn) {
                    connIndicator.className = 'conn-indicator online';
                    connTitle.textContent = 'WhatsApp Connected';
                    connDesc.textContent = 'Your bot is online and ready to send messages.';
                    connBadge.className = 'conn-badge online';
                    connBadge.textContent = 'Online';
                } else {
                    connIndicator.className = 'conn-indicator offline';
                    connTitle.textContent = 'WhatsApp Disconnected';
                    connDesc.textContent = 'Connect your WhatsApp to start sending messages.';
                    connBadge.className = 'conn-badge offline';
                    connBadge.textContent = 'Offline';
                }
            }

            updateConnectionView();
        } catch (error) {
            if (statusDot && statusText) {
                statusDot.className = 'status-dot offline pulsing';
                statusText.textContent = 'Offline';
            }
            if (connIndicator && connBadge) {
                connIndicator.className = 'conn-indicator offline';
                connTitle.textContent = 'Connection Error';
                connDesc.textContent = 'Unable to reach the server.';
                connBadge.className = 'conn-badge offline';
                connBadge.textContent = 'Error';
            }
            isConnected = false;
            isLoggedIn = false;
        }
    }

    function updateConnectionView() {
        if (!loggedInView) return; // Not on connection page

        if (isLoggedIn && isConnected) {
            loggedInView.classList.remove('hidden');
            loggedOutView.classList.add('hidden');
            disconnectedView.classList.add('hidden');
            stopQRPoll();
        } else if (isLoggedIn && !isConnected) {
            loggedInView.classList.add('hidden');
            loggedOutView.classList.add('hidden');
            disconnectedView.classList.remove('hidden');
            stopQRPoll();
        } else {
            loggedInView.classList.add('hidden');
            loggedOutView.classList.remove('hidden');
            disconnectedView.classList.add('hidden');
            startQRPoll();
        }
    }

    async function fetchQR() {
        if (!loggedOutView || loggedOutView.classList.contains('hidden')) return;

        try {
            const res = await apiFetch('/api/qr');
            if (!res.ok) {
                // Server returned error (404, 500, etc.) — skip JSON parse
                throw new Error(`QR endpoint returned ${res.status}`);
            }
            const data = await res.json();

            if (data.code) {
                // Client-side QR generation using qrcode-generator
                try {
                    var qr = qrcode(0, 'L');
                    qr.addData(data.code);
                    qr.make();
                    qrImage.src = qr.createDataURL(6, 4);
                } catch (qrErr) {
                    console.error('QR generation failed', qrErr);
                }
                qrDisplay.classList.remove('hidden');
                qrLoading.classList.add('hidden');
                qrError.classList.add('hidden');
            } else {
                if (!qrDisplay.classList.contains('hidden')) {
                    // Keep current QR
                } else {
                    qrLoading.classList.add('hidden');
                    qrError.classList.remove('hidden');
                }
            }
        } catch (e) {
            console.error('Failed to fetch QR', e);
        }
    }

    function startQRPoll() {
        if (qrPollInterval) return;
        fetchQR();
        qrPollInterval = trackedSetInterval(fetchQR, 5000);
    }

    function stopQRPoll() {
        if (qrPollInterval) {
            clearInterval(qrPollInterval);
            // Remove from tracked list
            const idx = _intervalIds.indexOf(qrPollInterval);
            if (idx > -1) _intervalIds.splice(idx, 1);
            qrPollInterval = null;
        }
    }

    async function fetchMetrics() {
        if (!metricSent) return;
        
        try {
            const res = await apiFetch('/api/metrics');
            const data = await res.json();
            metricSent.textContent = data.total_sent || '0';
            metricSent.classList.add('loaded');
            metricFailed.textContent = data.total_failed || '0';
            metricFailed.classList.add('loaded');
            metricScheduled.textContent = data.scheduled_count || '0';
            metricScheduled.classList.add('loaded');
        } catch(e) {
            console.error('Failed to fetch metrics', e);
        }
    }

    // Initial check and regular polling
    checkStatus();
    fetchMetrics();
    trackedSetInterval(checkStatus, 3000);
    trackedSetInterval(fetchMetrics, 5000);

    // ---- Helper ----
    function showFeedback(element, message, type) {
        if (!element) return;
        element.textContent = message;
        element.classList.remove('hidden');
        
        if (type === 'success') {
            element.style.background = 'rgba(37, 211, 102, 0.1)';
            element.style.color = '#25d366';
            element.style.border = '1px solid rgba(37, 211, 102, 0.2)';
            element.style.padding = '12px 16px';
            element.style.borderRadius = '12px';
            element.style.fontSize = '14px';
            element.style.fontWeight = '500';
            element.style.textAlign = 'center';
            element.style.marginTop = '16px';
        } else {
            element.style.background = 'rgba(239, 68, 68, 0.1)';
            element.style.color = '#ef4444';
            element.style.border = '1px solid rgba(239, 68, 68, 0.2)';
            element.style.padding = '12px 16px';
            element.style.borderRadius = '12px';
            element.style.fontSize = '14px';
            element.style.fontWeight = '500';
            element.style.textAlign = 'center';
            element.style.marginTop = '16px';
        }
        
        setTimeout(() => {
            element.classList.add('hidden');
        }, 5000);
    }

    // ---- Connection Handlers ----
    if (disconnectBtn) {
        disconnectBtn.addEventListener('click', async () => {
            try {
                const res = await apiFetch('/api/disconnect', { method: 'POST' });
                const data = await res.json();
                if (data.success) {
                    showFeedback(connFeedback, 'Disconnected successfully', 'success');
                    checkStatus();
                } else {
                    showFeedback(connFeedback, data.error || 'Failed to disconnect', 'error');
                }
            } catch (e) {
                showFeedback(connFeedback, 'Network error', 'error');
            }
        });
    }

    if (connectBtn) {
        connectBtn.addEventListener('click', async () => {
            try {
                const res = await apiFetch('/api/connect', { method: 'POST' });
                const data = await res.json();
                if (data.success) {
                    showFeedback(connFeedback, 'Connecting...', 'success');
                    checkStatus();
                } else {
                    showFeedback(connFeedback, data.error || 'Failed to connect', 'error');
                }
            } catch (e) {
                showFeedback(connFeedback, 'Network error', 'error');
            }
        });
    }

    if (logoutBtn) {
        logoutBtn.addEventListener('click', async () => {
            if (!confirm('Are you sure you want to logout? This will wipe your session data.')) return;
            try {
                const res = await apiFetch('/api/logout', { method: 'POST' });
                const data = await res.json();
                if (data.success) {
                    showFeedback(connFeedback, 'Logged out. Redirecting to scan...', 'success');
                    setTimeout(() => location.reload(), 2000);
                } else {
                    showFeedback(connFeedback, data.error || 'Failed to logout', 'error');
                }
            } catch (e) {
                showFeedback(connFeedback, 'Network error', 'error');
            }
        });
    }

    if (retryQRBtn) {
        retryQRBtn.addEventListener('click', () => {
            qrDisplay.classList.add('hidden');
            qrError.classList.add('hidden');
            qrLoading.classList.remove('hidden');
            fetchQR();
        });
    }

    // ---- Single Send Handler ----
    if (sendMessageForm) {
        sendMessageForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            if (!isConnected || !isLoggedIn) return showFeedback(singleFeedback, 'Bot is not connected/logged in', 'error');

            const to = document.getElementById('phoneNumber').value.trim();
            const message = document.getElementById('messageBody').value.trim();
            const fileInput = document.getElementById('fileAttachment');
            const file = fileInput.files[0];

            if (!to || (!message && !file)) {
                return showFeedback(singleFeedback, 'Please provide a phone number and message/file', 'error');
            }

            const ogText = sendBtn.innerHTML;
            sendBtn.disabled = true;
            sendBtn.classList.add('opacity-60');
            sendBtn.querySelector('.btn-text').textContent = 'Sending...';

            try {
                let res;
                if (file) {
                    const formData = new FormData();
                    formData.append('to', to);
                    formData.append('message', message);
                    formData.append('file', file);
                    res = await apiFetch('/api/send', { method: 'POST', body: formData });
                } else {
                    res = await apiFetch('/api/send', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ to, message })
                    });
                }

                const data = await res.json();
                if (res.ok && data.success) {
                    showFeedback(singleFeedback, 'Message sent successfully!', 'success');
                    document.getElementById('messageBody').value = ''; 
                    fileInput.value = ''; 
                } else {
                    throw new Error(data.error || 'Failed to send');
                }
            } catch (error) {
                showFeedback(singleFeedback, error.message, 'error');
            } finally {
                sendBtn.disabled = false;
                sendBtn.classList.remove('opacity-60');
                sendBtn.innerHTML = ogText;
            }
        });
    }

    // ---- Bulk Send Handler ----
    if (bulkForm) {
        bulkForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            if (!isConnected || !isLoggedIn) return showFeedback(bulkFeedback, 'Bot is not connected/logged in', 'error');

            const delay = parseInt(document.getElementById('bulkDelay').value) || 2000;
            const csvData = document.getElementById('bulkData').value.trim();

            if (!csvData) return showFeedback(bulkFeedback, 'Please provide recipient data', 'error');

            const lines = csvData.split('\n');
            const messages = [];

            for (let i = 0; i < lines.length; i++) {
                const line = lines[i].trim();
                if(!line) continue;
                
                const commaIndex = line.indexOf(',');
                if(commaIndex === -1) {
                    return showFeedback(bulkFeedback, `Invalid format on line ${i+1}. Expected: Phone, Message`, 'error');
                }
                
                const to = line.substring(0, commaIndex).trim();
                const msg = line.substring(commaIndex + 1).trim();
                
                if(!to || !msg) {
                    return showFeedback(bulkFeedback, `Invalid data on line ${i+1}. Missing phone or message.`, 'error');
                }
                messages.push({ to, message: msg });
            }

            const ogText = bulkBtn.innerHTML;
            bulkBtn.disabled = true;
            bulkBtn.classList.add('opacity-60');
            bulkBtn.querySelector('.btn-text').textContent = 'Dispatching...';

            try {
                const res = await apiFetch('/api/bulk-send', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ messages, interval_ms: delay })
                });

                const data = await res.json();
                if (res.ok && data.success) {
                    showFeedback(bulkFeedback, `Started dispatching ${messages.length} messages in the background!`, 'success');
                    document.getElementById('bulkData').value = '';
                } else {
                    throw new Error(data.error || 'Failed to start bulk send');
                }
            } catch (error) {
                showFeedback(bulkFeedback, error.message, 'error');
            } finally {
                bulkBtn.disabled = false;
                bulkBtn.classList.remove('opacity-60');
                bulkBtn.innerHTML = ogText;
            }
        });
    }

    // ---- Schedule Handler ----
    if (scheduleForm) {
        scheduleForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            
            const to = document.getElementById('schPhoneNumber').value.trim();
            const timeVal = document.getElementById('schTime').value;
            const message = document.getElementById('schMessageBody').value.trim();

            if (!to || !timeVal || !message) {
                return showFeedback(schFeedback, 'Please fill out all fields', 'error');
            }

            const isoTime = new Date(timeVal).toISOString();

            const ogText = schBtn.innerHTML;
            schBtn.disabled = true;
            schBtn.classList.add('opacity-60');
            schBtn.querySelector('.btn-text').textContent = 'Scheduling...';

            try {
                const res = await apiFetch('/api/schedule', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ to, message, scheduled_for: isoTime })
                });

                const data = await res.json();
                if (res.ok && data.success) {
                    showFeedback(schFeedback, 'Message successfully scheduled!', 'success');
                    document.getElementById('schMessageBody').value = '';
                    document.getElementById('schTime').value = '';
                } else {
                    throw new Error(data.error || 'Failed to schedule');
                }
            } catch (error) {
                showFeedback(schFeedback, error.message, 'error');
            } finally {
                schBtn.disabled = false;
                schBtn.classList.remove('opacity-60');
                schBtn.innerHTML = ogText;
            }
        });
    }
});
