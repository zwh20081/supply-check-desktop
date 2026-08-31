import { makeStyles, shorthands, tokens } from '@fluentui/react-components';

/**
 * 玻璃质感统一在这里。面板本身不滚动（整页滚动），
 * 所以 backdrop-filter 不会触发持续 GPU 重绘。
 */
export const useAppStyles = makeStyles({
  glass: {
    backdropFilter: 'blur(64px) saturate(2)',
    WebkitBackdropFilter: 'blur(64px) saturate(2)',
    boxShadow: tokens.shadow16,
    ...shorthands.border('1px', 'solid', tokens.colorNeutralStroke2),
  },

  shell: {
    minHeight: '100dvh',
    position: 'relative',
    zIndex: 1,
  },

  workspace: {
    width: 'min(1180px, 100%)',
    // 兜底：任何子元素都不该把整页撑宽产生横向滚动
    maxWidth: '100%',
    overflowX: 'hidden',
    ...shorthands.margin('0', 'auto'),
    ...shorthands.padding('0', '32px', '80px'),
    '@media (max-width: 1024px)': {
      ...shorthands.padding('0', '24px', '64px'),
    },
    '@media (max-width: 768px)': {
      ...shorthands.padding('0', '16px', '48px'),
    },
    '@media (max-width: 480px)': {
      ...shorthands.padding('0', '12px', '40px'),
    },
  },

  // --- 顶部：品牌 + 步骤 TabList + 语言 ---
  topbar: {
    position: 'sticky',
    top: 0,
    zIndex: 20,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    columnGap: '24px',
    rowGap: '8px',
    flexWrap: 'wrap',
    marginBottom: '28px',
    ...shorthands.padding('12px', '16px'),
    ...shorthands.borderRadius(tokens.borderRadiusXLarge),
    backgroundColor: tokens.colorNeutralBackground1,
    // 三块挤一行会溢出：先收紧间距，再让 TabList 独占一行
    '@media (max-width: 900px)': {
      columnGap: '12px',
      marginBottom: '20px',
    },
    '@media (max-width: 640px)': {
      ...shorthands.padding('10px', '12px'),
      ...shorthands.borderRadius(tokens.borderRadiusLarge),
    },
  },

  /* 窄屏下 TabList 换行独占整行，并允许横向滚动而不是压扁标签 */
  topbarTabs: {
    '@media (max-width: 640px)': {
      order: 3,
      width: '100%',
      overflowX: 'auto',
      // 隐藏滚动条但保留滚动能力，标签本身已是明确的可点区域
      scrollbarWidth: 'none',
      '::-webkit-scrollbar': { display: 'none' },
    },
  },

  langPicker: {
    minWidth: '108px',
    '@media (max-width: 640px)': { minWidth: '92px' },
  },

  brand: { display: 'flex', alignItems: 'center', columnGap: '10px' },

  brandMark: {
    display: 'grid',
    placeItems: 'center',
    width: '32px',
    height: '32px',
    flexShrink: 0,
    color: tokens.colorNeutralForegroundOnBrand,
    backgroundColor: tokens.colorBrandBackground,
    ...shorthands.borderRadius(tokens.borderRadiusCircular),
  },

  brandText: { display: 'grid', lineHeight: 1.25 },

  // --- 步骤内容区 ---
  step: {
    display: 'grid',
    // grid 子项默认 min-width:auto，长模型名会把列撑开导致整块横向溢出。
    // 显式约束成 minmax(0,1fr) 才会让内部网格与警告条正确收缩。
    gridTemplateColumns: 'minmax(0, 1fr)',
    rowGap: '20px',
    ...shorthands.padding('28px'),
    ...shorthands.borderRadius(tokens.borderRadiusXLarge),
    backgroundColor: tokens.colorNeutralBackground1,
    '@media (max-width: 768px)': { ...shorthands.padding('18px'), rowGap: '16px' },
    '@media (max-width: 480px)': {
      ...shorthands.padding('14px'),
      ...shorthands.borderRadius(tokens.borderRadiusLarge),
    },
  },

  stepHead: { display: 'grid', rowGap: '4px' },

  // 进入动画：只动 transform / opacity
  stepEnter: {
    animationName: {
      from: { opacity: 0, transform: 'translate3d(0, 12px, 0)' },
      to: { opacity: 1, transform: 'translate3d(0, 0, 0)' },
    },
    animationDuration: tokens.durationGentle,
    animationTimingFunction: tokens.curveDecelerateMid,
    animationFillMode: 'both',
    '@media (prefers-reduced-motion: reduce)': { animationDuration: '1ms' },
  },

  // --- 底部导航条 ---
  stepNav: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    columnGap: '12px',
    rowGap: '10px',
    flexWrap: 'wrap',
    ...shorthands.padding('16px', '0', '0'),
    ...shorthands.borderTop('1px', 'solid', tokens.colorNeutralStroke2),
    // 窄屏改为纵向：按钮撑满，主操作放在最上，说明文字沉底
    '@media (max-width: 560px)': {
      flexDirection: 'column',
      alignItems: 'stretch',
      '> button': { width: '100%' },
    },
  },

  // 顶条与页脚跟随步骤内容一起进场，避免整页同时闪现
  topbarEnter: {
    animationName: {
      from: { opacity: 0, transform: 'translate3d(0, -10px, 0)' },
      to: { opacity: 1, transform: 'translate3d(0, 0, 0)' },
    },
    animationDuration: tokens.durationSlow,
    animationTimingFunction: tokens.curveDecelerateMid,
    animationFillMode: 'both',
    '@media (prefers-reduced-motion: reduce)': { animationDuration: '1ms' },
  },

  /* 卡片按次序错开进场。用 CSS 变量传序号，避免为每张卡生成一个类。 */
  stagger: {
    animationName: {
      from: { opacity: 0, transform: 'translate3d(0, 14px, 0)' },
      to: { opacity: 1, transform: 'translate3d(0, 0, 0)' },
    },
    animationDuration: tokens.durationGentle,
    animationTimingFunction: tokens.curveDecelerateMid,
    animationFillMode: 'both',
    animationDelay: 'calc(var(--i, 0) * 60ms)',
    '@media (prefers-reduced-motion: reduce)': {
      animationDuration: '1ms',
      animationDelay: '0ms',
    },
  },

  /* 可点卡片的物理反馈：悬停微抬，按下回落。只动 transform。 */
  pressable: {
    transitionProperty: 'transform, box-shadow',
    transitionDuration: tokens.durationNormal,
    transitionTimingFunction: tokens.curveEasyEase,
    ':hover': { transform: 'translate3d(0, -2px, 0)' },
    ':active': { transform: 'scale(0.985)' },
    '@media (prefers-reduced-motion: reduce)': {
      transitionDuration: '1ms',
      ':hover': { transform: 'none' },
    },
  },

  navHint: { color: tokens.colorNeutralForeground3 },

  // 上游报错里的长 URL / token 没有空格，不强制断行仍会溢出
  wrapAnywhere: { overflowWrap: 'anywhere', minWidth: 0 },
  grow: { flexGrow: 1 },
  // rowGap 是必需的：换行后没有它两行会直接贴在一起
  row: { display: 'flex', alignItems: 'center', columnGap: '10px', rowGap: '8px', flexWrap: 'wrap' },

  /* 滑条与数值徽章同行。Slider 的 thumb 会伸到轨道两端之外，
     所以要留右侧内边距，否则拖到底时 thumb 和徽章会顶到容器边缘。 */
  sliderRow: {
    display: 'flex',
    alignItems: 'center',
    columnGap: '16px',
    ...shorthands.padding('4px', '10px', '4px', '2px'),
  },

  // Slider 自带固定宽度，要显式撑满才会跟着容器伸缩
  slider: { flexGrow: 1, minWidth: 0, width: 'auto' },

  sliderValue: {
    minWidth: '2.25rem',
    flexShrink: 0,
    fontVariantNumeric: 'tabular-nums',
  },

  colGap: { display: 'grid', rowGap: '14px' },

  // --- 协议卡 ---
  /* 四个协议：宽屏一行四列，中屏两列，窄屏单列。
     用固定列数而非 auto-fit，避免出现 3+1 的孤儿行。 */
  providerGrid: {
    display: 'grid',
    gridTemplateColumns: 'repeat(4, minmax(0, 1fr))',
    columnGap: '12px',
    rowGap: '12px',
    '@media (max-width: 1024px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' },
    '@media (max-width: 560px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
  },

  providerCard: { cursor: 'pointer' },

  providerCardOn: {
    ...shorthands.borderColor(tokens.colorBrandStroke1),
    backgroundColor: tokens.colorNeutralBackground1Hover,
  },

  // --- 表单 ---
  formGrid: {
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)',
    columnGap: '16px',
    rowGap: '16px',
    '@media (max-width: 900px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
  },

  // --- 模型选择 ---
  modelGrid: {
    display: 'grid',
    gridTemplateColumns: 'repeat(auto-fill, minmax(230px, 1fr))',
    columnGap: '8px',
    rowGap: '8px',
    maxHeight: '380px',
    overflowY: 'auto',
    // 右侧留出滚动条宽度，否则滚动条会压住最右一列
    ...shorthands.padding('4px', '10px', '4px', '2px'),
    // 230px 的最小列宽在窄屏会横向溢出，且模型列表本身要留足纵向空间
    '@media (max-width: 560px)': {
      gridTemplateColumns: 'minmax(0, 1fr)',
      maxHeight: '300px',
    },
  },

  modelItem: {
    display: 'flex',
    alignItems: 'center',
    minWidth: 0,
    ...shorthands.padding('6px', '10px'),
    ...shorthands.borderRadius(tokens.borderRadiusMedium),
    ':hover': { backgroundColor: tokens.colorNeutralBackground1Hover },
    // Checkbox 及其 label 都得能收缩，否则长模型 ID 会撑开单元格
    '> label': { minWidth: 0, width: '100%' },
    '& span': { minWidth: 0 },
  },

  modelItemOn: { backgroundColor: tokens.colorBrandBackground2 },
  ellipsis: { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 },
  mono: { fontFamily: tokens.fontFamilyMonospace },

  // --- 报告指标 ---
  metricGrid: {
    display: 'grid',
    gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))',
    columnGap: '8px',
    rowGap: '8px',
    // 五个指标在窄屏挤成一行会截断数字，改为两列
    '@media (max-width: 560px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' },
  },

  metric: {
    display: 'grid',
    rowGap: '2px',
    ...shorthands.padding('12px', '14px'),
    ...shorthands.borderRadius(tokens.borderRadiusMedium),
    backgroundColor: tokens.colorNeutralBackground2,
  },

  // --- 运行视图 ---
  runView: { display: 'grid', rowGap: '18px', justifyItems: 'center', textAlign: 'center' },
  runMeter: { width: '100%', maxWidth: '520px', display: 'grid', rowGap: '10px' },

  scoreBig: {
    fontSize: tokens.fontSizeHero900,
    fontWeight: tokens.fontWeightSemibold,
    lineHeight: 1,
    fontVariantNumeric: 'tabular-nums',
  },

  // --- 报告浏览 ---
  reportSplit: {
    display: 'grid',
    gridTemplateColumns: '240px minmax(0, 1fr)',
    columnGap: '18px',
    alignItems: 'start',
    '@media (max-width: 900px)': { gridTemplateColumns: 'minmax(0, 1fr)', rowGap: '14px' },
  },

  modelNav: {
    display: 'grid',
    rowGap: '4px',
    maxHeight: '520px',
    overflowY: 'auto',
    // 折叠成单列后，模型列表若仍占 520px 会把探针详情推到屏幕外
    '@media (max-width: 900px)': { maxHeight: '220px' },
  },

  modelNavItem: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    columnGap: '8px',
    width: '100%',
    textAlign: 'left',
    ...shorthands.padding('8px', '10px'),
    ...shorthands.borderRadius(tokens.borderRadiusMedium),
    ...shorthands.border('1px', 'solid', 'transparent'),
    backgroundColor: 'transparent',
    cursor: 'pointer',
    ':hover': { backgroundColor: tokens.colorNeutralBackground1Hover },
  },

  modelNavOn: {
    backgroundColor: tokens.colorNeutralBackground1Hover,
    ...shorthands.borderColor(tokens.colorNeutralStroke1),
  },

  evidence: {
    maxHeight: '300px',
    overflowY: 'auto',
    ...shorthands.margin('0'),
    ...shorthands.padding('12px'),
    ...shorthands.borderRadius(tokens.borderRadiusMedium),
    backgroundColor: tokens.colorNeutralBackground3,
    fontFamily: tokens.fontFamilyMonospace,
    fontSize: tokens.fontSizeBase200,
    lineHeight: tokens.lineHeightBase300,
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-word',
    color: tokens.colorNeutralForeground2,
  },
});
