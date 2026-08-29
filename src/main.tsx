import React, { useEffect, useSyncExternalStore } from 'react';
import ReactDOM from 'react-dom/client';
import { FluentProvider } from '@fluentui/react-components';
import App from './App';
import { I18nProvider } from './i18n';
import { DARK_FLYOUT, FLYOUT_VAR, LIGHT_FLYOUT, darkGlassTheme, lightGlassTheme } from './theme';
import './styles.css';

const colorScheme = window.matchMedia('(prefers-color-scheme: dark)');

function subscribeColorScheme(callback: () => void) {
  colorScheme.addEventListener('change', callback);
  return () => colorScheme.removeEventListener('change', callback);
}

function Root() {
  const dark = useSyncExternalStore(subscribeColorScheme, () => colorScheme.matches);

  // 浮层通过 portal 挂在 body 下，不在 FluentProvider 的子树里，
  // 所以这个变量必须下到 :root 才能被弹出层取到。
  useEffect(() => {
    document.documentElement.style.setProperty(FLYOUT_VAR, dark ? DARK_FLYOUT : LIGHT_FLYOUT);
  }, [dark]);

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
