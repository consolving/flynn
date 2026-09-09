import Dispatcher from 'dashboard/dispatcher';
import Config from 'dashboard/config';
import { extend } from 'marbles/utils';

var createAppRoute = function (appID, route) {
	var client = Config.client;
	client.getApp(appID).then(function (args) {
		var app = args[0];
		var data = extend({
			type: 'http',
			service: app.name + '-web',
			// Requesting a Let's Encrypt certificate for this exact domain by
			// default gives new routes HTTPS out of the box. If provisioning
			// fails (e.g. the domain isn't delegated to the configured ACME
			// DNS provider), the route itself still works over plain HTTP.
			acme_domain: route.domain
		}, route);
		return client.createAppRoute(appID, data);
	}).then(function (args) {
		// createAppRoute swallows failures (it dispatches
		// CREATE_APP_ROUTE_FAILED and resolves with undefined), so only
		// attempt provisioning if the route was actually created.
		if (!args) {
			return;
		}
		var res = args[0];
		return client.provisionACMECert([res.domain]).catch(function (err) {
			console.error('Failed to provision Let\'s Encrypt certificate for '+ res.domain +':', err);
		});
	});
};

var deleteAppRoute = function (appID, routeType, routeID) {
	var client = Config.client;
	client.deleteAppRoute(appID, routeType, routeID);
};

Dispatcher.register(function (event) {
	switch (event.name) {
	case 'CREATE_APP_ROUTE':
		createAppRoute(event.appID, event.data);
		break;

	case 'DELETE_APP_ROUTE':
		deleteAppRoute(event.appID, event.routeType, event.routeID);
		break;
	}
});
