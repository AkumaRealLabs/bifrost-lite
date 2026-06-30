import { GetUserAccessProfilesResponse } from "@enterprise/lib/types/accessProfile";

// OSS build has no access-profile backend — return undefined data so consumers
// Lite fallback: access profiles are not part of the shipped surface.
export const useGetUserAccessProfilesQuery = (
	_userId: string,
	_opts?: { skip?: boolean; pollingInterval?: number },
): {
	data: GetUserAccessProfilesResponse | undefined;
	isLoading: boolean;
	isError: boolean;
	error: null;
} => ({
	data: undefined,
	isLoading: false,
	isError: false,
	error: null,
});
