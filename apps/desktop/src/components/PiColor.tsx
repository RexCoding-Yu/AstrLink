import { forwardRef, type SVGProps } from "react";

// The bundled icon library only includes Pi's older monochrome mark.
export const PiColor = forwardRef<
  SVGSVGElement,
  SVGProps<SVGSVGElement> & { size?: number | string }
>(function PiColor({ size = "1em", style, ...props }, ref) {
  return (
    <svg
      height={size}
      ref={ref}
      style={{ flex: "none", lineHeight: 1, ...style }}
      viewBox="0 0 24 24"
      width={size}
      xmlns="http://www.w3.org/2000/svg"
      {...props}
    >
      <title>Pi</title>
      <path fill="#e58a7a" d="M1 1h16.5v11H12V6.5H1V1Z" />
      <path fill="#4f8eb4" d="M1 6.5h5.5V12H12v5.5H6.5V23H1V6.5Z" />
      <path fill="#eab65d" d="M17.5 12H23v11h-5.5V12Z" />
    </svg>
  );
});
