import { HospitalOnboarding } from "@/components/HospitalOnboarding";

export default async function HospitalPage({
  searchParams,
}: {
  searchParams: Promise<{ tenantId?: string }>;
}) {
  const { tenantId } = await searchParams;
  return <HospitalOnboarding initialTenantId={tenantId} />;
}
