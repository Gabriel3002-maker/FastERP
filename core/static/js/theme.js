/**
 * Theme Manager - Dark/Light Mode Support
 * Profesional Corporate Theme
 */

class ThemeManager {
  constructor() {
    this.STORAGE_KEY = 'fasterp-theme';
    this.LIGHT = 'light';
    this.DARK = 'dark';
    this.AUTO = 'auto';

    this.init();
  }

  init() {
    this.createThemeToggle();
    this.applyTheme(this.getTheme());

    // Listen for system theme changes
    if (window.matchMedia) {
      window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
        if (this.getTheme() === this.AUTO) {
          this.updateThemeToggle();
        }
      });
    }
  }

  getTheme() {
    const stored = localStorage.getItem(this.STORAGE_KEY);
    return stored || this.AUTO;
  }

  setTheme(theme) {
    localStorage.setItem(this.STORAGE_KEY, theme);
    this.applyTheme(theme);
    this.updateThemeToggle();
  }

  applyTheme(theme) {
    const html = document.documentElement;
    let isDark = false;

    if (theme === this.AUTO) {
      isDark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
      html.style.colorScheme = isDark ? 'dark' : 'light';
    } else if (theme === this.DARK) {
      isDark = true;
      html.style.colorScheme = 'dark';
    } else {
      isDark = false;
      html.style.colorScheme = 'light';
    }

    html.setAttribute('data-theme', isDark ? 'dark' : 'light');

    if (isDark) {
      // PrimeNG Dark Theme (Aura Dark)
      html.style.setProperty('--primary', '#818cf8');
      html.style.setProperty('--primary-hover', '#a5b4fc');
      html.style.setProperty('--bg-primary', '#0f172a');
      html.style.setProperty('--bg-secondary', '#1e293b');
      html.style.setProperty('--text-primary', '#f8fafc');
      html.style.setProperty('--text-secondary', '#94a3b8');
      html.style.setProperty('--border', '#334155');
      html.style.setProperty('--surface', '#1e293b');
      html.style.setProperty('--accent', '#818cf8');
      html.style.setProperty('--hover', 'rgba(129, 140, 248, 0.12)');
    } else {
      // PrimeNG Light Theme (Aura Light)
      html.style.setProperty('--primary', '#4f46e5');
      html.style.setProperty('--primary-hover', '#4338ca');
      html.style.setProperty('--bg-primary', '#f8fafc');
      html.style.setProperty('--bg-secondary', '#ffffff');
      html.style.setProperty('--text-primary', '#0f172a');
      html.style.setProperty('--text-secondary', '#64748b');
      html.style.setProperty('--border', '#e2e8f0');
      html.style.setProperty('--surface', '#ffffff');
      html.style.setProperty('--accent', '#6366f1');
      html.style.setProperty('--hover', 'rgba(99, 102, 241, 0.06)');
    }
  }

  createThemeToggle() {
    if (document.querySelector('.theme-toggle')) return;

    const toggle = document.createElement('button');
    toggle.className = 'theme-toggle';
    toggle.title = 'Toggle theme';
    toggle.setAttribute('aria-label', 'Toggle dark/light theme');

    this.updateThemeToggleIcon(toggle);

    toggle.addEventListener('click', () => {
      const current = this.getTheme();
      let next;

      if (current === this.LIGHT) {
        next = this.DARK;
      } else if (current === this.DARK) {
        next = this.AUTO;
      } else {
        next = this.LIGHT;
      }

      this.setTheme(next);
    });

    document.body.appendChild(toggle);
  }

  updateThemeToggle() {
    const toggle = document.querySelector('.theme-toggle');
    if (toggle) {
      this.updateThemeToggleIcon(toggle);
    }
  }

  updateThemeToggleIcon(toggle) {
    const theme = this.getTheme();
    let icon = '🌙'; // Auto/Dark
    let title = 'Dark mode';

    if (theme === this.LIGHT) {
      icon = '☀️';
      title = 'Light mode';
    } else if (theme === this.AUTO) {
      icon = '🔄';
      title = 'Auto mode';
    }

    toggle.textContent = icon;
    toggle.title = title;
  }

  isDarkMode() {
    const theme = this.getTheme();
    if (theme === this.DARK) return true;
    if (theme === this.LIGHT) return false;

    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
  }
}

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
  new ThemeManager();
});

// Also initialize immediately for early support
if (document.readyState === 'loading') {
  new ThemeManager();
}
