import { createFileRoute, Outlet, useChildMatches } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import GovernancePage from "./page";

function RouteComponent() {
	const hasVirtualKeysAccess = useRbac(RbacResource.VirtualKeys, RbacOperation.View);

	const childMatches = useChildMatches();
	if (!hasVirtualKeysAccess) {
		return <NoPermissionView entity="access" />;
	}
	return childMatches.length === 0 ? <GovernancePage /> : <Outlet />;
}

export const Route = createFileRoute("/workspace/governance")({
	component: RouteComponent,
});
