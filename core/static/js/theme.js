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

    if (theme === this.AUTO) {
      html.style.colorScheme = 'light dark';
      // Let browser handle via prefers-color-scheme
    } else if (theme === this.DARK) {
      html.style.colorScheme = 'dark';
      html.style.setProperty('--primary', '#579DFF');
      html.style.setProperty('--primary-hover', '#85B8FF');
      html.style.setProperty('--bg-primary', '#161A1D');
      html.style.setProperty('--bg-secondary', '#22272B');
      html.style.setProperty('--text-primary', '#B6C2CF');
      html.style.setProperty('--text-secondary', '#8590A2');
      html.style.setProperty('--border', '#38414B');
    } else {
      html.style.colorScheme = 'light';
      html.style.setProperty('--primary', '#0052CC');
      html.style.setProperty('--primary-hover', '#003DA8');
      html.style.setProperty('--bg-primary', '#F7F8FA');
      html.style.setProperty('--bg-secondary', '#FFFFFF');
      html.style.setProperty('--text-primary', '#172B4D');
      html.style.setProperty('--text-secondary', '#626F86');
      html.style.setProperty('--border', '#DCDFE4');
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
