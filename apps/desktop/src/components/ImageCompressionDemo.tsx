import { Fragment, useState } from "react";
import { motion, useReducedMotion } from "motion/react";

import {
  imageCompressionModes,
  type ImageCompressionMode,
} from "@/failure-policy-model";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import {
  formatImageBytes,
  imageCompressionText,
} from "@/request-trajectory-model";
import { chipToneClass } from "@/trajectory-chip";
import { useLoopStep } from "@/use-loop-step";
import { FlowDemoStage } from "./FlowDemoStage";
import { SegmentedControl } from "./SegmentedControl";
import { Badge } from "./ui/badge";

/**
 * One example request: each image's decoded bytes and the WebP each mode
 * sends instead, null keeping the image. A flat screenshot comes out
 * smallest lossless in either mode. A link carries no image bytes, so the
 * record does not count it.
 */
const exampleImages = [
  {
    id: "screenshot",
    type: "png",
    bytes: 1_258_291,
    webp: { lossless: 395_776, lossy: 395_776 },
  },
  {
    id: "photo",
    type: "jpg",
    bytes: 662_118,
    webp: { lossless: null, lossy: 248_320 },
  },
  {
    id: "animation",
    type: "gif",
    bytes: 307_610,
    webp: { lossless: null, lossy: null },
  },
  {
    id: "link",
    type: null,
    bytes: null,
    webp: { lossless: null, lossy: null },
  },
] as const;

type ExampleImage = (typeof exampleImages)[number];

const inlineImages = exampleImages.filter(
  (image): image is Extract<ExampleImage, { bytes: number }> =>
    image.bytes !== null,
);

// Each image sets off at an odd step and comes out of AstrLink at the next
// one, about when it passes the logo. The record fills at the last step.
const flowTimes = [
  0, 600, 1100, 1300, 1800, 2000, 2500, 2700, 3200, 3600,
] as const;
const flowHold = 2400;
const laneTop = (index: number) => 28 + index * 24;

/** The file an image travels as, or its WebP when webp holds that size. */
function FileLabel({
  image,
  webp,
}: {
  image: ExampleImage;
  webp: number | null;
}) {
  const t = useT();
  const name = t(`imageCompression.demo.files.${image.id}`);
  if (image.bytes === null) return name;
  return (
    <>
      {`${name}.${webp === null ? image.type : "webp"}`}
      <span className="font-normal opacity-70">
        {formatImageBytes(webp ?? image.bytes)}
      </span>
    </>
  );
}

/**
 * The example request's images pass through AstrLink one by one and line up
 * on the model's side. Those a smaller WebP replaces come out highlighted;
 * the rest come out unchanged.
 */
function CompressionFlow({ mode }: { mode: ImageCompressionMode }) {
  const t = useT();
  const reducedMotion = useReducedMotion();
  const { step, cycle } = useLoopStep(flowTimes, flowHold, true);
  const departed = Math.ceil(step / 2);
  const arrived = Math.floor(step / 2);
  const recorded = step === flowTimes.length - 1;
  const summary = {
    mode,
    quality: null,
    images: inlineImages.length,
    compressed: inlineImages.filter((image) => image.webp[mode] !== null)
      .length,
    beforeBytes: inlineImages.reduce((sum, image) => sum + image.bytes, 0),
    afterBytes: inlineImages.reduce(
      (sum, image) => sum + (image.webp[mode] ?? image.bytes),
      0,
    ),
  };
  return (
    <>
      <FlowDemoStage
        className="h-32"
        data-arrived={arrived}
        data-visual="compression"
        from={t("imageCompression.demo.client")}
        gatewayTop={52}
        to={t("imageCompression.demo.model")}
      >
        {/* What the client sent stays outlined where each image started. */}
        {exampleImages.map((image, index) => (
          <span
            className="absolute left-[3%] flex h-5 items-center gap-1 rounded-sm border border-dashed px-1.5 text-micro whitespace-nowrap text-muted-foreground"
            key={image.id}
            style={{ top: laneTop(index) }}
          >
            <FileLabel image={image} webp={null} />
          </span>
        ))}
        {exampleImages.map((image, index) => {
          const out = index < arrived;
          const compressed = out && image.webp[mode] !== null;
          return (
            <motion.span
              // Sliding the anchor across the card as it travels ends it
              // flush right, whatever its width.
              animate={
                index < departed
                  ? { left: "97%", x: "-100%", opacity: 1 }
                  : { left: "3%", x: "0%", opacity: 1 }
              }
              className={cn(
                "absolute flex h-5 items-center gap-1 rounded-sm border px-1.5 text-micro font-medium whitespace-nowrap",
                compressed
                  ? "border-primary/40 bg-accent text-primary"
                  : "bg-background text-foreground",
              )}
              data-arrived={out || undefined}
              data-compressed={compressed || undefined}
              data-image={image.id}
              initial={
                reducedMotion ? false : { left: "3%", x: "0%", opacity: 0 }
              }
              key={`${cycle}-${image.id}`}
              style={{ top: laneTop(index) }}
              transition={{
                default: {
                  duration: reducedMotion ? 0 : 0.9,
                  ease: "easeInOut",
                },
                opacity: { duration: reducedMotion ? 0 : 0.25 },
              }}
            >
              <FileLabel
                image={image}
                webp={compressed ? image.webp[mode] : null}
              />
            </motion.span>
          );
        })}
      </FlowDemoStage>
      {/* The row the request record's trace shows for this request. It keeps
          its place while hidden, so the popover holds its height. */}
      <div aria-hidden="true" className="grid gap-1">
        <span className="text-micro text-muted-foreground">
          {`${t("records.title")} › ${t("records.tabTrajectory")}`}
        </span>
        <div
          className="grid rounded-md border border-dashed"
          data-compression-record={recorded ? "shown" : "pending"}
        >
          <motion.span
            animate={{ opacity: recorded ? 1 : 0 }}
            className="-m-px flex min-w-0 items-center gap-2 rounded-md border bg-card px-2 py-1.5 text-micro"
            initial={false}
            transition={{ duration: reducedMotion ? 0 : 0.35 }}
          >
            <Badge
              className={cn("h-5", chipToneClass("COMPRESS", "ok", "subtle"))}
              variant="secondary"
            >
              {t("trajectory.chips.COMPRESS")}
            </Badge>
            {/* Lines break between the row's parts, never inside one. */}
            <span className="min-w-0">
              {imageCompressionText(summary)
                .split(" · ")
                .map((part, index) => (
                  <Fragment key={part}>
                    {index > 0 ? "\u00a0· " : null}
                    <span className="whitespace-nowrap">{part}</span>
                  </Fragment>
                ))}
            </span>
          </motion.span>
        </div>
      </div>
    </>
  );
}

/**
 * The image compression explanation. It opens on the panel's mode; its
 * switcher changes only the example.
 */
export function ImageCompressionDemo({
  mode: savedMode,
}: {
  mode: ImageCompressionMode;
}) {
  const t = useT();
  const [mode, setMode] = useState(savedMode);
  return (
    <div className="grid gap-2.5" data-compression-demo={mode}>
      <SegmentedControl
        label={t("imageCompression.mode")}
        options={imageCompressionModes.map((value) => ({
          value,
          label: t(`imageCompression.modes.${value}`),
        }))}
        value={mode}
        onValueChange={setMode}
      />
      <CompressionFlow key={mode} mode={mode} />
      <p className="m-0 text-xs leading-relaxed text-muted-foreground">
        {t(`imageCompression.demo.text.${mode}`)}
      </p>
      <p className="m-0 text-xs leading-relaxed text-muted-foreground">
        {t("imageCompression.demo.notes")}
      </p>
    </div>
  );
}
