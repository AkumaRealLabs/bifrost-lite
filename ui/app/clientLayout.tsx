import FullPageLoader from "@/components/fullPageLoader";
import NotAvailableBanner from "@/components/notAvailableBanner";
import ProgressProvider from "@/components/progressBar";
import Sidebar from "@/components/sidebar";
import { ThemeProvider } from "@/components/themeProvider";
import TrialExpiryBanner from "@/components/trialExpiryBanner";
import { SidebarProvider } from "@/components/ui/sidebar";
import { getErrorMessage, ReduxProvider, useGetCoreConfigQuery } from "@/lib/store";
import { BifrostConfig } from "@/lib/types/config";
import { RbacProvider, useRbacContext } from "@enterprise/lib/contexts/rbacContext";
import { useLocation, useMatches } from "@tanstack/react-router";
import { NuqsAdapter } from "nuqs/adapters/tanstack-router";
import { useEffect } from "react";
import { CookiesProvider } from "react-cookie";
import { toast, Toaster } from "sonner";

function AppContent({ children }: { children: React.ReactNode }) {
	const matches = useMatches();
	// publicShell: route declares it's a static, auth-free page that should
	// always render MinimalShell — no chrome, no auth probe, no API calls.
	const publicShell = matches.some((m) => (m.staticData as { publicShell?: boolean } | undefined)?.publicShell === true);

	const {
		data: bifrostConfig,
		error,
		isLoading,
	} = useGetCoreConfigQuery(
		{},
		{
			skip: publicShell,
		},
	);

	// Permissions are restored from sessionStorage (async) and refreshed from the
	// API. Until that first resolve, useRbac() returns false for everything, which
	// would briefly collapse the sidebar to a single tab and flash NoPermissionView
	// on the active route. Gate the full dashboard chrome on it; the cached read is
	// a single frame so this is imperceptible. Minimal/public shells don't use RBAC
	// and are handled by the early returns below.
	const { isLoading: rbacLoading } = useRbacContext();

	useEffect(() => {
		if (error) {
			toast.error(getErrorMessage(error));
		}
	}, [error]);

	if (publicShell) {
		return <MinimalShell>{children}</MinimalShell>;
	}

	if (rbacLoading) {
		return <FullPageLoader />;
	}

	return (
		<CookiesProvider>
			<SidebarProvider>
				<Sidebar />
				<div className="dark:bg-card custom-scrollbar content-container my-[0.5rem] mr-[0.5rem] h-[calc(100dvh-1rem)] w-full min-w-xl overflow-auto rounded-md border border-gray-200 bg-white px-10 dark:border-zinc-800">
					<TrialExpiryBanner />
					<main className="custom-scrollbar content-container-inner relative mx-auto flex h-full min-h-0 flex-col overflow-y-hidden p-4">
						{isLoading ? <FullPageLoader /> : <FullPage config={bifrostConfig}>{children}</FullPage>}
					</main>
				</div>
			</SidebarProvider>
		</CookiesProvider>
	);
}

// MinimalShell renders a centered container without sidebar
// or any dashboard-config fetches. Used by static public routes.
function MinimalShell({ children }: { children: React.ReactNode }) {
	return (
		<div className="dark:bg-card custom-scrollbar content-container my-[0.5rem] h-[calc(100dvh-1rem)] w-full overflow-auto rounded-md border border-gray-200 bg-white px-10 dark:border-zinc-800">
			<main className="custom-scrollbar content-container-inner relative mx-auto flex h-full min-h-0 flex-col overflow-y-hidden p-4">
				{children}
			</main>
		</div>
	);
}

function FullPage({ config, children }: { config: BifrostConfig | undefined; children: React.ReactNode }) {
	const pathname = useLocation({ select: (l) => l.pathname });
	if (config && config.is_db_connected) {
		return children;
	}
	if (config && config.is_logs_connected && pathname.startsWith("/workspace/logs")) {
		return children;
	}
	return <NotAvailableBanner />;
}

export function ClientLayout({ children }: { children: React.ReactNode }) {
	return (
		<ProgressProvider>
			<ThemeProvider attribute="class" defaultTheme="system" enableSystem>
				<Toaster closeButton />
				<ReduxProvider>
					<NuqsAdapter>
						<RbacProvider>
							<AppContent>{children}</AppContent>
						</RbacProvider>
					</NuqsAdapter>
				</ReduxProvider>
			</ThemeProvider>
		</ProgressProvider>
	);
}
