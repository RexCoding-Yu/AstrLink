import { useEffect, useMemo, useRef, useState } from "react";

import { Brain, Boxes, Flask, SlidersHorizontal } from "@/components/icons";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ClassifierDryRunPanel } from "./ClassifierDryRunPanel";
import { ClassifierSettingsPanel } from "./ClassifierSettingsPanel";
import { ModelRoutingFieldCatalogPanel } from "./ModelRoutingFieldCatalogPanel";
import { useT } from "./i18n";
import { ModelRoutingGroupsPanel } from "./ModelRoutingGroupsPanel";
import { PageHeader } from "./PageHeader";
import type { RoutableService } from "./service-model";

export type IntelligentRoutingView =
  | "groups"
  | "fieldCatalog"
  | "classifier"
  | "dryRun";

export function IntelligentRouting({
  initialView = "groups",
  services,
  protocols = [],
  ready,
  onDirtyChange,
}: {
  initialView?: IntelligentRoutingView;
  services: readonly RoutableService[];
  /** core capabilities' protocol IDs — the value domain of the `protocol` rule field. */
  protocols?: readonly string[];
  ready: boolean;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  const [view, setView] = useState<IntelligentRoutingView>(initialView);
  // One dirty slot per tab. The three panels share the navigation guard, but
  // not the same source of truth: a single slot let whichever panel unmounted
  // last write `false` over a sibling's unsaved edit, so leaving the page no
  // longer warned and the edit was lost silently.
  const [tabDirty, setTabDirty] = useState<
    Record<IntelligentRoutingView, boolean>
  >({ groups: false, fieldCatalog: false, classifier: false, dryRun: false });
  const dirty = useMemo(
    () => Object.values(tabDirty).some(Boolean),
    [tabDirty],
  );

  // The dirty map covers every tab so a future panel cannot silently lose its
  // slot; only the three draft-holding panels consume theirs today.
  const setTabDirtyChange = useMemo(
    () =>
      Object.fromEntries(
        (["groups", "fieldCatalog", "classifier", "dryRun"] as const).map(
          (key) => [
            key,
            (next: boolean) =>
              setTabDirty((current) =>
                current[key] === next ? current : { ...current, [key]: next },
              ),
          ],
        ),
      ) as Pick<
        Record<IntelligentRoutingView, (next: boolean) => void>,
        "groups" | "fieldCatalog" | "classifier" | "dryRun"
      >,
    [],
  );

  // Only the aggregate leaves this component, and only when it changes.
  const reportedDirty = useRef(false);
  useEffect(() => {
    if (reportedDirty.current === dirty) return;
    reportedDirty.current = dirty;
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-clip"
      data-testid="intelligent-routing-panel"
    >
      <PageHeader className="mb-0" title={t("intelligentRouting.title")} />
      <Tabs
        className="min-h-0 min-w-0 flex-1 gap-3 overflow-y-clip"
        onValueChange={(value) => setView(value as IntelligentRoutingView)}
        value={view}
      >
        <TabsList
          scrollable
          aria-label={t("intelligentRouting.workspace")}
          className="min-w-0 shrink-0"
        >
          <TabsTrigger value="groups">
            <Boxes aria-hidden="true" />
            {t("intelligentRouting.tabs.groups")}
          </TabsTrigger>
          <TabsTrigger value="fieldCatalog">
            <SlidersHorizontal aria-hidden="true" />
            {t("intelligentRouting.tabs.fieldCatalog")}
          </TabsTrigger>
          <TabsTrigger value="classifier">
            <Brain aria-hidden="true" />
            {t("intelligentRouting.tabs.classifier")}
          </TabsTrigger>
          <TabsTrigger value="dryRun">
            <Flask aria-hidden="true" />
            {t("intelligentRouting.tabs.dryRun")}
          </TabsTrigger>
        </TabsList>
        {/* The group editor keeps its draft, so it must outlive a tab switch. */}
        <TabsContent
          forceMount
          hidden={view !== "groups"}
          value="groups"
          className="gutter-scroller flex min-h-0 flex-1 flex-col overflow-y-auto pb-1"
          data-tab-scroller
        >
          <ModelRoutingGroupsPanel
            ready={ready}
            services={services}
            protocols={protocols}
            onDirtyChange={setTabDirtyChange.groups}
          />
        </TabsContent>
        {/* Editable since ASTR-18: the built-in table stays read-only, the
            custom-dimension block below it holds a draft. Force-mounted like the
            siblings so a tab switch keeps the scroll position. */}
        <TabsContent
          forceMount
          hidden={view !== "fieldCatalog"}
          value="fieldCatalog"
          className="gutter-scroller flex min-h-0 flex-1 flex-col overflow-y-auto pb-1"
          data-tab-scroller
        >
          <ModelRoutingFieldCatalogPanel
            ready={ready}
            protocols={protocols}
            onDirtyChange={setTabDirtyChange.fieldCatalog}
          />
        </TabsContent>
        {/* Every panel holds an unsaved draft, so all three outlive a switch. */}
        <TabsContent
          forceMount
          hidden={view !== "classifier"}
          value="classifier"
          className="gutter-scroller flex min-h-0 flex-1 flex-col overflow-y-auto pb-1"
          data-tab-scroller
        >
          <ClassifierSettingsPanel
            ready={ready}
            services={services}
            onDirtyChange={setTabDirtyChange.classifier}
          />
        </TabsContent>
        <TabsContent
          forceMount
          hidden={view !== "dryRun"}
          value="dryRun"
          className="gutter-scroller flex min-h-0 flex-1 flex-col overflow-y-auto pb-1"
          data-tab-scroller
        >
          <ClassifierDryRunPanel ready={ready} protocols={protocols} />
        </TabsContent>
      </Tabs>
    </div>
  );
}
