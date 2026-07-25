import React from 'react';

interface PlumBlossomProps {
  size?: number;
  className?: string;
  title?: string;
  style?: React.CSSProperties;
}

/**
 * PlumBlossom — botanically accurate five-petal plum blossom (Prunus mume / 梅花)
 * carried on a dark gnarled branch with two pointed leaves, for the classic 梅花
 * composition.
 *
 * Key features that distinguish from cherry blossom (樱花):
 * - 5 round petals with NO notch at the tip
 * - Deep crimson/pink colour (not pale blush)
 * - Many prominent stamens (15-20 filaments with yellow anthers)
 * - Flowers sit directly on a dark woody branch (brown SVG paths)
 * - Two green leaves on the branch
 * - Compact, slightly cupped shape
 *
 * SVG path-based petals with bezier curves for organic roundness.
 */
const PlumBlossom: React.FC<PlumBlossomProps> = ({
  size = 64,
  className,
  title = 'iCode 梅花',
  style,
}) => {
  const gid = 'pm';

  // Petal path: a rounded, slightly cupped shape — wider at base, rounded tip.
  // Each petal is rotated 72° from the centre. Slight overlap for realism.
  const petalPath = (angle: number) => {
    const rad = (angle * Math.PI) / 180;
    const cosA = Math.cos(rad);
    const sinA = Math.sin(rad);
    const tx = (x: number, y: number) => 50 + x * cosA - y * sinA;
    const ty = (x: number, y: number) => 50 + x * sinA + y * cosA;

    // Cubic bezier control points defining a round petal
    return [
      `M ${tx(0, 4)} ${ty(0, 4)}`,
      `C ${tx(11, -4)} ${ty(11, -4)}, ${tx(12, -16)} ${ty(12, -16)}, ${tx(3, -24)} ${ty(3, -24)}`,
      `C ${tx(-1, -27)} ${ty(-1, -27)}, ${tx(-8, -25)} ${ty(-8, -25)}, ${tx(-11, -18)} ${ty(-11, -18)}`,
      `C ${tx(-13, -12)} ${ty(-13, -12)}, ${tx(-11, -4)} ${ty(-11, -4)}, ${tx(0, 4)} ${ty(0, 4)}`,
      'Z',
    ].join(' ');
  };

  // Stamens: 16 thin filaments radiating from centre, each tipped with a small anther
  const stamenCount = 16;
  const stamens = Array.from({ length: stamenCount }, (_, i) => {
    const a = ((i * 360) / stamenCount + 7) * (Math.PI / 180); // slight offset so they don't align with petals
    const innerR = 7;
    const outerR = 15 + (i % 3) * 1.5; // slight length variation
    return {
      x1: 50 + Math.cos(a) * innerR,
      y1: 50 + Math.sin(a) * innerR,
      x2: 50 + Math.cos(a) * outerR,
      y2: 50 + Math.sin(a) * outerR,
      ax: 50 + Math.cos(a) * (outerR + 2),
      ay: 50 + Math.sin(a) * (outerR + 2),
    };
  });

  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 100 100"
      className={className}
      style={style}
      role="img"
      aria-label={title}
    >
      <title>{title}</title>
      <defs>
        {/* Petal gradient: light crimson at base → deeper crimson at edges */}
        <radialGradient id={`${gid}-pg`} cx="50%" cy="55%" r="60%">
          <stop offset="0%" stopColor="#F06292" />
          <stop offset="60%" stopColor="#E8467A" />
          <stop offset="100%" stopColor="#AD1457" />
        </radialGradient>
        {/* Centre gradient */}
        <radialGradient id={`${gid}-cg`} cx="50%" cy="45%" r="50%">
          <stop offset="0%" stopColor="#FDD835" />
          <stop offset="100%" stopColor="#F9A825" />
        </radialGradient>
      </defs>

      {/* Branch — a dark woody stem curving down-left with a small twig,
          drawn BEHIND the petals so the blossom appears to sit on it. */}
      <g fill="none" stroke="#6D4C41" strokeLinecap="round">
        <path d="M 50 64 C 47 74, 42 83, 34 92" strokeWidth={3.2} />
        <path d="M 48 74 C 54 73, 58 71, 63 67" strokeWidth={2} />
      </g>

      {/* Leaves — two pointed plum leaves carried on the branch. */}
      <g fill="#66BB6A" stroke="#388E3C" strokeWidth={0.6}>
        <ellipse cx={33} cy={90} rx={5.5} ry={2.6} transform="rotate(-32 33 90)" />
        <ellipse cx={62} cy={68} rx={4.6} ry={2.2} transform="rotate(34 62 68)" />
      </g>

      {/* Five petals — slightly overlapping, deep crimson */}
      <g>
        {[0, 1, 2, 3, 4].map((i) => (
          <path
            key={i}
            d={petalPath(i * 72)}
            fill={`url(#${gid}-pg)`}
            stroke="#AD1457"
            strokeWidth={0.6}
            strokeLinejoin="round"
          />
        ))}
      </g>

      {/* Stamen filaments — thin white/pink lines */}
      <g>
        {stamens.map((s, i) => (
          <line
            key={`f-${i}`}
            x1={s.x1}
            y1={s.y1}
            x2={s.x2}
            y2={s.y2}
            stroke="#F8BBD0"
            strokeWidth={0.5}
            strokeLinecap="round"
          />
        ))}
      </g>

      {/* Anthers — small yellow dots at filament tips */}
      <g>
        {stamens.map((s, i) => (
          <circle
            key={`a-${i}`}
            cx={s.ax}
            cy={s.ay}
            r={1.2}
            fill="#FDD835"
            stroke="#F57F17"
            strokeWidth={0.3}
          />
        ))}
      </g>

      {/* Centre pistil */}
      <circle cx={50} cy={50} r={5.5} fill={`url(#${gid}-cg)`} stroke="#F57F17" strokeWidth={0.5} />
    </svg>
  );
};

export default PlumBlossom;
