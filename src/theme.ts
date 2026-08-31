import {
  createDarkTheme, createLightTheme,
  type BrandVariants, type Theme,
} from '@fluentui/react-components';

/** 青绿品牌坡道，对应原设计的 accent。 */
const brand: BrandVariants = {
  10: '#001410',
  20: '#00201B',
  30: '#00332B',
  40: '#004339',
  50: '#005448',
  60: '#006657',
  70: '#007867',
  80: '#0A8B78',
  90: '#2A9E8B',
  100: '#45B19E',
  110: '#5EC4B1',
  120: '#78D5C4',
  130: '#95E3D5',
  140: '#B3EEE4',
  150: '#D0F6F0',
  160: '#EBFBF8',
};

/**
 * 把 Fluent 的 surface token 改成半透明，让 Acrylic 和画布光晕能透过来。
 * 只动 Background1/2（承载卡片与画布），交互态 token 保持不透明以免 hover 时糊掉。
 */
function glassify(theme: Theme, layers: Partial<Theme>): Theme {
  return { ...theme, ...layers };
}

export const darkGlassTheme = glassify(createDarkTheme(brand), {
  colorNeutralBackground1: 'rgba(22, 28, 27, 0.26)',
  colorNeutralBackground1Hover: 'rgba(42, 51, 49, 0.52)',
  colorNeutralBackground1Pressed: 'rgba(32, 40, 38, 0.62)',
  colorNeutralBackground2: 'rgba(16, 21, 20, 0.20)',
  colorNeutralBackground3: 'rgba(12, 16, 15, 0.16)',
  colorNeutralBackgroundStatic: 'rgba(28, 34, 33, 0.42)',
  colorNeutralStroke1: 'rgba(255, 255, 255, 0.12)',
  colorNeutralStroke2: 'rgba(255, 255, 255, 0.08)',
  colorNeutralStrokeAccessible: 'rgba(255, 255, 255, 0.34)',
});

export const lightGlassTheme = glassify(createLightTheme(brand), {
  colorNeutralBackground1: 'rgba(255, 255, 255, 0.34)',
  colorNeutralBackground1Hover: 'rgba(255, 255, 255, 0.58)',
  colorNeutralBackground1Pressed: 'rgba(246, 249, 248, 0.68)',
  colorNeutralBackground2: 'rgba(252, 254, 253, 0.24)',
  colorNeutralBackground3: 'rgba(248, 251, 250, 0.20)',
  colorNeutralBackgroundStatic: 'rgba(255, 255, 255, 0.46)',
  colorNeutralStroke1: 'rgba(13, 30, 27, 0.10)',
  colorNeutralStroke2: 'rgba(13, 30, 27, 0.07)',
  colorNeutralStrokeAccessible: 'rgba(13, 30, 27, 0.42)',
});
