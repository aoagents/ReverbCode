import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	clearUnreadNotifications,
	fetchUnreadNotifications,
	markAllNotificationsRead,
	markNotificationRead,
	removeUnreadNotification,
	unreadNotificationsQueryKey,
} from "../lib/notifications";

export function useNotificationsQuery() {
	return useQuery({
		queryKey: unreadNotificationsQueryKey,
		queryFn: fetchUnreadNotifications,
		retry: 1,
	});
}

export function useMarkNotificationReadMutation() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: markNotificationRead,
		onSuccess: (notification) => {
			removeUnreadNotification(queryClient, notification.id);
		},
	});
}

export function useMarkAllNotificationsReadMutation() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: markAllNotificationsRead,
		onSuccess: () => {
			clearUnreadNotifications(queryClient);
			void queryClient.invalidateQueries({ queryKey: unreadNotificationsQueryKey });
		},
	});
}
