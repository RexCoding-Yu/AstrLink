import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";

import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { Boxes, Key, Link, Plus, Route, Server } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

import { confirmProviderImport, dismissProviderImport } from "./bridge";
import { i18n } from "./i18n";
import {
  providerImportInput,
  type ProviderImportPlan,
} from "./provider-import-model";
import type { Service } from "./service-model";
import {
  httpServiceKindLabel,
  protocolLabel,
  serviceAuthLabels,
} from "./service-presets";

function Detail({
  icon,
  label,
  children,
  className,
}: {
  icon: ReactNode;
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex min-w-0 items-start gap-2.5", className)}>
      <span
        aria-hidden="true"
        className="mt-0.5 inline-flex size-4 shrink-0 items-center justify-center text-muted-foreground"
      >
        {icon}
      </span>
      <div className="grid min-w-0 gap-1">
        <span className="text-xs text-muted-foreground">{label}</span>
        {children}
      </div>
    </div>
  );
}

/** Confirms a provider an `astrlink://` link asked to add. */
export function ProviderImportDialog({
  id,
  plan,
  onAdded,
  onClose,
}: {
  id: string;
  plan: ProviderImportPlan;
  onAdded: (service: Service) => void;
  onClose: () => void;
}) {
  const t = i18n.t.bind(i18n);
  const [name, setName] = useState(plan.defaultName);
  const [secret, setSecret] = useState("");
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const active = useRef(true);
  const pending = useRef(false);
  const keyMissing = plan.needsKey && !secret.trim();

  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);

  const cancel = () => {
    if (pending.current) return;
    dismissProviderImport(id).catch((cause: unknown) =>
      console.error("Unable to dismiss the provider link", cause),
    );
    onClose();
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (pending.current || !name.trim() || keyMissing) return;
    pending.current = true;
    setAdding(true);
    setError(null);
    try {
      const record = await confirmProviderImport(
        id,
        providerImportInput(plan, name, secret),
      );
      if (active.current) onAdded(record.service);
    } catch (cause) {
      if (active.current) {
        setError(
          t("providerImport.failed", {
            message: cause instanceof Error ? cause.message : String(cause),
          }),
        );
      }
    } finally {
      pending.current = false;
      if (active.current) setAdding(false);
    }
  };

  const auth = serviceAuthLabels[plan.auth.scheme];
  return (
    <Dialog open onOpenChange={(open) => !open && cancel()}>
      <DialogContent
        showCloseButton={!adding}
        className="w-[calc(100vw_-_2rem)] max-w-none sm:max-w-2xl"
      >
        <DialogHeader className="text-left">
          <DialogTitle className="flex items-center gap-2.5">
            <Link aria-hidden="true" className="size-5 text-muted-foreground" />
            {t("providerImport.title")}
          </DialogTitle>
          <DialogDescription>
            {t("providerImport.description")}
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(event) => void submit(event)}>
          <div className="grid gap-3 rounded-md border bg-muted/40 p-3 min-[540px]:grid-cols-2">
            <Detail
              className="min-[540px]:col-span-2"
              icon={<Server className="size-4" />}
              label={t("services.apiAddress")}
            >
              <span className="break-all text-sm font-medium">{plan.host}</span>
              <code className="break-all text-xs text-muted-foreground">
                {plan.baseURL}
              </code>
            </Detail>
            <Detail
              icon={<ServiceKindIcon kind={plan.kind} size={16} />}
              label={t("services.serviceType")}
            >
              <span className="text-xs">{httpServiceKindLabel(plan.kind)}</span>
            </Detail>
            <Detail
              icon={<Key className="size-4" />}
              label={t("services.authScheme")}
            >
              <span className="text-xs">
                {plan.auth.scheme === "custom_header"
                  ? `${auth} · ${plan.auth.header_name}`
                  : auth}
              </span>
              {plan.keyIncluded ? (
                <span className="text-xs text-muted-foreground">
                  {t("providerImport.keyIncluded")}{" "}
                  {plan.keyHint ? (
                    <span className="font-mono">{plan.keyHint}</span>
                  ) : null}
                </span>
              ) : null}
            </Detail>
            <Detail
              icon={<Route className="size-4" />}
              label={t("services.tabProtocols")}
            >
              <span className="text-xs">
                {plan.capabilities
                  .map((capability) => protocolLabel(capability.protocol))
                  .join(" · ")}
              </span>
            </Detail>
            <Detail
              icon={<Boxes className="size-4" />}
              label={t("providerImport.models")}
            >
              {plan.models.length > 0 ? (
                <span
                  className="line-clamp-2 break-all text-xs"
                  title={plan.models.join("\n")}
                >
                  {t("providerImport.modelCount", {
                    count: plan.models.length,
                  })}
                  {" · "}
                  <span className="font-mono text-muted-foreground">
                    {plan.models.join(", ")}
                  </span>
                </span>
              ) : (
                <span className="text-xs text-muted-foreground">
                  {t("providerImport.noModels")}
                </span>
              )}
            </Detail>
          </div>
          {plan.insecure && (
            <FormMessage tone="warning">
              {t("providerImport.insecure")}
            </FormMessage>
          )}
          <div className="grid gap-4 min-[540px]:grid-cols-2">
            <Field
              htmlFor="provider-import-name"
              label={t("services.serviceName")}
            >
              <Input
                id="provider-import-name"
                value={name}
                onChange={(event) => setName(event.currentTarget.value)}
                maxLength={128}
                disabled={adding}
                required
              />
            </Field>
            {plan.needsKey && (
              <Field
                htmlFor="provider-import-key"
                label="API Key"
                hint={t("providerImport.keyNeeded")}
              >
                <Input
                  id="provider-import-key"
                  autoComplete="new-password"
                  type="password"
                  value={secret}
                  onChange={(event) => setSecret(event.currentTarget.value)}
                  placeholder={t("services.apiKeyPaste")}
                  maxLength={16_384}
                  disabled={adding}
                  required
                />
              </Field>
            )}
          </div>
          {error && <FormMessage tone="error">{error}</FormMessage>}
          <DialogFooter className="border-t pt-4 min-[540px]:flex-row min-[540px]:justify-end">
            <Button
              type="button"
              variant="outline"
              disabled={adding}
              onClick={cancel}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              disabled={adding || !name.trim() || keyMissing}
            >
              <Plus />
              {adding ? t("providerImport.adding") : t("providerImport.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
