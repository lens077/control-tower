import { ink, state } from "@/styles/tokens";

/**
 * 品牌标记:一个细线圆,右上沿一段紫罗兰弧 —— 「颜色只住在边缘」的缩写。
 * 与顶栏的虹彩发丝线是同一句话。
 */
export function BrandMark({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="12" cy="12" r="9" stroke={ink.strong} strokeWidth="1.25" />
      <path
        d="M12 3a9 9 0 0 1 9 9"
        stroke={state.active}
        strokeWidth="2.25"
        strokeLinecap="round"
      />
      <circle cx="12" cy="12" r="2" fill={ink.strong} />
    </svg>
  );
}
