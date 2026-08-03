document.addEventListener('DOMContentLoaded', () => {
    initWizard();
    initTabs();
    initExpandableRows();
    initModelNameAutoGen();
    initExpertToggle();
});

function initWizard() {
    const wizard = document.querySelector('.wizard-form');
    if (!wizard) return;

    const steps = wizard.querySelectorAll('.wizard-step-panel');
    const stepButtons = document.querySelectorAll('.wizard-step');
    // data-start-step lets a server-rendered validation-error re-render
    // land the user directly on the step containing the problem field
    // (e.g. the Review step's expert overrides) instead of always
    // starting over at step 1.
    let currentStep = parseInt(wizard.dataset.startStep || '0', 10);
    if (!(currentStep >= 0 && currentStep < steps.length)) currentStep = 0;

    function showStep(n) {
        steps.forEach((s, i) => { s.style.display = i === n ? 'block' : 'none'; });
        stepButtons.forEach((b, i) => {
            b.classList.remove('active', 'completed');
            if (i < n) b.classList.add('completed');
            if (i === n) b.classList.add('active');
        });
    }

    stepButtons.forEach((btn, i) => {
        btn.addEventListener('click', () => {
            if (i < currentStep || (btn.classList.contains('completed'))) {
                currentStep = i;
                showStep(i);
            }
        });
    });

    wizard.addEventListener('click', (e) => {
        const nextBtn = e.target.closest('.wizard-next');
        if (nextBtn) {
            if (currentStep < steps.length - 1) {
                currentStep++;
                showStep(currentStep);
            }
            return;
        }
        const prevBtn = e.target.closest('.wizard-prev');
        if (prevBtn) {
            if (currentStep > 0) {
                currentStep--;
                showStep(currentStep);
            }
        }
    });

    showStep(currentStep);
}

function initTabs() {
    document.querySelectorAll('.tabs').forEach(tabGroup => {
        const tabs = tabGroup.querySelectorAll('.tab');
        if (!tabs.length) return;

        tabs[0].classList.add('active');
        const tabId = tabs[0].dataset.tab;
        const content = document.getElementById(`tab-${tabId}`);
        if (content) content.style.display = 'block';

        tabs.forEach(tab => {
            tab.addEventListener('click', () => {
                tabs.forEach(t => t.classList.remove('active'));
                tab.classList.add('active');
                const id = tab.dataset.tab;
                tabGroup.parentElement.querySelectorAll('.tab-content').forEach(c => c.style.display = 'none');
                const panel = document.getElementById(`tab-${id}`);
                if (panel) panel.style.display = 'block';
            });
        });
    });
}

function initExpandableRows() {
    document.querySelectorAll('.gpu-row').forEach(row => {
        row.addEventListener('click', () => {
            const expand = row.nextElementSibling;
            if (expand && expand.classList.contains('gpu-expand')) {
                expand.classList.toggle('open');
            }
        });
    });

    document.querySelectorAll('.expand-toggle').forEach(btn => {
        btn.addEventListener('click', (e) => {
            e.stopPropagation();
            const target = document.querySelector(btn.dataset.target);
            if (target) target.classList.toggle('open');
            btn.textContent = target && target.classList.contains('open') ? 'Collapse' : 'Expand';
        });
    });
}

function initModelNameAutoGen() {
    const modelIdInput = document.querySelector('[name="model-id"]');
    const modelNameInput = document.querySelector('[name="model-name"]');
    if (!modelIdInput || !modelNameInput) return;

    modelIdInput.addEventListener('input', () => {
        if (modelNameInput.dataset.dirty === 'true') return;
        const parts = modelIdInput.value.split('/');
        let name = parts[parts.length - 1] || parts[0] || '';
        name = name.toLowerCase().replace(/[._]/g, '-').replace(/[^a-z0-9-]/g, '').replace(/--+/g, '-').replace(/^-|-$/g, '');
        if (name) modelNameInput.value = name;
    });

    modelNameInput.addEventListener('input', () => {
        modelNameInput.dataset.dirty = 'true';
    });
}

function initExpertToggle() {
    const toggle = document.querySelector('.expert-toggle input');
    const sections = document.querySelectorAll('.expert-section');
    if (!toggle) return;

    toggle.addEventListener('change', () => {
        sections.forEach(s => s.classList.toggle('visible', toggle.checked));
    });
}

function addPromotionNamespace() {
    const container = document.getElementById('promotion-namespaces-container');
    if (!container) return;
    const idx = container.querySelectorAll('.promotion-ns-entry').length;
    const div = document.createElement('div');
    div.className = 'form-group promotion-ns-entry';
    div.innerHTML = `
        <label>Promotion Namespace ${idx + 1}</label>
        <input type="text" name="promotion-namespace-${idx}" placeholder="production" class="form-input">
        <button type="button" class="btn btn-sm btn-danger" onclick="this.parentElement.remove()" style="margin-top:4px">Remove</button>
    `;
    container.appendChild(div);
}
