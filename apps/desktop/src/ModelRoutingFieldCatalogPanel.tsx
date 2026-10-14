import { Panel, PanelHeader } from "./components/Panel";
import { HelpPopover } from "./components/HelpPopover";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./components/ui/table";
import { getRoutingSettings } from "./bridge";
import { useEffect, useState } from "react";
import type { RoutingSettings } from "./failure-policy-model";
import { useT } from "./i18n";
import {
  modelRoutingCatalog,
  modelRoutingCatalogLabelDomain,
} from "./model-routing-catalog-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

/**
 * 「字段类型」页签：规则条件能用的元信息字段一览。
 *
 * 只读。全局标签的权威编辑资源在后端尚无落地契约（`RoutingSettings` 里没有
 * catalog 字段，`contracts/control-api.openapi.yaml` 亦无端点），因此这里只呈现
 * 代码固定的内建目录与当前生效的 `task_type` 标签域，不提供编辑入口 ——
 * 造一个写不回去的编辑框会让用户白填一次。
 */
export function ModelRoutingFieldCatalogPanel({ ready }: { ready: boolean }) {
  const t = useT();
  const entries = modelRoutingCatalog();
  const [settings, setSettings] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
    null,
  );
  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    if (!ready) return;
    let active = true;
    void getRoutingSettings()
      .then((loaded) => {
        if (!active) return;
        setSettings(loaded);
        setLoadError(null);
      })
      .catch((caught: unknown) => {
        if (active)
          setLoadError(caught instanceof Error ? caught.message : null);
      });
    return () => {
      active = false;
    };
  }, [ready, setSettings]);

  const groups = settings?.model_routing_groups ?? [];
  // 组级覆盖优先；全部为空时回落到内建默认域。这与编辑器里
  // `group.task_type_labels?.length ? … : modelRoutingTaskTypes` 的判定一致。
  const labelDomain = modelRoutingCatalogLabelDomain(
    groups.map((group) => group.task_type_labels),
  );

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-clip"
      data-testid="model-routing-field-catalog-panel"
    >
      <Panel className="flex min-h-0 flex-1 flex-col">
        <PanelHeader
          actions={
            <HelpPopover
              label={t("modelRoutingGroup.catalogGlobalLabelHintLabel")}
            >
              {t("modelRoutingGroup.catalogGlobalLabelHint")}
            </HelpPopover>
          }
        >
          <h3 className="text-sm font-semibold">
            {t("modelRoutingGroup.catalogTitle")}
          </h3>
        </PanelHeader>
        <div className="gutter-scroller min-h-0 overflow-y-auto">
          <Table className="text-xs">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="px-4">
                  {t("modelRoutingGroup.catalogField")}
                </TableHead>
                <TableHead className="px-4">
                  {t("modelRoutingGroup.catalogKind")}
                </TableHead>
                <TableHead className="px-4">
                  {t("modelRoutingGroup.catalogSource")}
                </TableHead>
                <TableHead className="px-4">
                  {t("modelRoutingGroup.catalogOperators")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((entry) => (
                <TableRow
                  key={entry.field}
                  data-model-routing-catalog-field={entry.field}
                >
                  <TableCell className="px-4">
                    {t(`modelRouting.fields.${entry.field}`)}
                    <span className="ml-1.5 font-mono text-muted-foreground">
                      {entry.field}
                    </span>
                  </TableCell>
                  <TableCell className="px-4">
                    {t(`modelRoutingGroup.fieldKind.${entry.kind}`)}
                    {entry.values ? (
                      <span className="ml-1.5 text-muted-foreground">
                        {entry.values
                          .map((value) =>
                            t(`modelRouting.values.${entry.field}.${value}`),
                          )
                          .join(" / ")}
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell className="px-4">
                    {t(`modelRouting.fieldSource.${entry.source}`)}
                  </TableCell>
                  <TableCell className="px-4 font-mono text-muted-foreground">
                    {entry.operators.join(", ")}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </Panel>
      <Panel className="flex min-h-0 shrink-0 flex-col">
        <PanelHeader>
          <h3 className="text-sm font-semibold">
            {t("modelRoutingGroup.catalogLabelsTitle")}
          </h3>
        </PanelHeader>
        <div className="flex flex-wrap gap-1.5 px-4 pb-3">
          {labelDomain.map((label) => (
            <span
              className="rounded-sm bg-muted px-1.5 py-0.5 text-xs"
              key={label}
            >
              {t(`modelRouting.values.task_type.${label}`)}
            </span>
          ))}
        </div>
      </Panel>
      {loadError ? (
        <p className="text-xs text-danger-foreground" role="alert">
          {loadError}
        </p>
      ) : null}
    </div>
  );
}
