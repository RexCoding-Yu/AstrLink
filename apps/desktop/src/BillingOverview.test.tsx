// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { WorkspaceSnapshotProvider } from "./workspace-snapshots";
import { useBillingSummary } from "./BillingOverview";
import {
  billingAmount,
  type BillingAmounts,
  type BillingSummary,
} from "./pricing-model";

const mocks = vi.hoisted(() => ({
  getBillingSummary: vi.fn(),
  getServiceBilling: vi.fn(),
}));
vi.mock("./pricing-bridge", () => mocks);

let root: Root;
let container: HTMLDivElement;

function amounts(amount: string, requests: number): BillingAmounts {
  return {
    amount_usd: amount,
    priced: requests,
    unpriced: 0,
    pending: 0,
    revalued: 0,
    requests,
  };
}

const summary: BillingSummary = {
  from: "2026-09-01",
  to: "2026-10-01",
  ...amounts("18.42", 10),
  by_model: [],
  by_token: [],
  by_service: [],
};
beforeEach(() => {
  vi.clearAllMocks();
  mocks.getBillingSummary.mockResolvedValue(summary);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

function OverviewBilling({
  from,
  revision,
}: {
  from: string;
  revision: number;
}) {
  const billing = useBillingSummary({
    from,
    to: summary.to,
    ready: true,
    revision,
  });
  return (
    <output>{billing.summary ? billingAmount(billing.summary) : ""}</output>
  );
}

async function render({
  visible = true,
  revision = 0,
  from = summary.from,
  session = "core-1",
} = {}) {
  await act(async () =>
    root.render(
      <WorkspaceSnapshotProvider sessionKey={session}>
        {visible ? <OverviewBilling from={from} revision={revision} /> : null}
      </WorkspaceSnapshotProvider>,
    ),
  );
}

it("retains the total across navigation and refresh until the new amount arrives", async () => {
  await render();
  expect(container.textContent).toContain("$18.42");
  await render({ visible: false });
  let finish!: (value: BillingSummary) => void;
  mocks.getBillingSummary.mockReturnValueOnce(
    new Promise((resolve) => {
      finish = resolve;
    }),
  );
  await render();
  expect(container.textContent).toContain("$18.42");
  await act(async () => finish({ ...summary, amount_usd: "20.00" }));
  expect(container.textContent).toContain("$20.00");
  mocks.getBillingSummary.mockReturnValue(new Promise(() => {}));
  await render({ revision: 1 });
  expect(container.textContent).toContain("$20.00");
});

it("does not reuse an amount from another range or Core session", async () => {
  await render();
  mocks.getBillingSummary.mockReturnValue(new Promise(() => {}));
  await render({ from: "2026-08-01" });
  expect(container.textContent).not.toContain("$18.42");
  await render({ session: "core-2" });
  expect(container.textContent).not.toContain("$18.42");
  expect(container.textContent).not.toContain("$0.00");
});
