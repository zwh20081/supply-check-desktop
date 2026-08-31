import React, { useSyncExternalStore } from 'react';
import ReactDOM from 'react-dom/client';
import { FluentProvider } from '@fluentui/react-components';
import App from './App';
import { I18nProvider } from './i18n';
import { darkGlassTheme, lightGlassTheme } from './theme';
import './styles.css';

const colorScheme = window.matchMedia('(prefers-color-scheme: dark)');

function subscribeColorScheme(callback: () => void) {
  colorScheme.addEventListener('change', callback);
  return () => colorScheme.removeEventListener('change', callback);
}

function Root() {
  const dark = useSyncExternalStore(subscribeColorScheme, () => colorScheme.matches);

  return (
    // FluentProvider 默认会刷不透明背景色，置空才能让 Acrylic 和 body 光晕透上来。
    <FluentProvider theme={dark ? darkGlassTheme : lightGlassTheme} style={{ backgroundColor: 'transparent' }}>
      <I18nProvider>
        <App />
      </I18nProvider>
    </FluentProvider>
  );
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <Root />
  </React.StrictMode>,
);
