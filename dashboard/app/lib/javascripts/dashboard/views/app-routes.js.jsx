import { assertEqual } from 'marbles/utils';
import { default as AppRoutesStore, shouldHTTPS } from '../stores/app-routes';
import ExternalLink from './external-link';
import RouteLink from './route-link';

function getAppRoutesStoreId (props) {
	return {
		appId: props.appId
	};
}

function getState (props) {
	var state = {
		appStoreId: getAppRoutesStoreId(props)
	};

	var appRoutesState = AppRoutesStore.getState(state.appStoreId);
	state.routes = appRoutesState.routes;

	return state;
}

function formatDate (dateString) {
	if (!dateString) return '';
	var date = new Date(dateString);
	return date.toLocaleDateString() + ' ' + date.toLocaleTimeString();
}

var AppRoutes = React.createClass({
	displayName: "Views.AppRoutes",

	render: function () {
		var getAppPath = this.props.getAppPath;
		return (
			<section className="app-routes">
				<header>
					<h2>Routes</h2>
				</header>

				<ul>
					{this.state.routes.map(function (route) {
						var certDetails = route.certificate_details;
						return (
							<li key={route.id || route.domain}>
								<ExternalLink href={(shouldHTTPS(route) ? 'https://' : 'http://') + route.domain + route.path}>
									{route.domain}{route.path}
								</ExternalLink>
								{route.id ? (
									<RouteLink path={getAppPath("/routes/:type/:route/delete", {route: route.id, type: route.type, domain: route.domain})}>
										<i className="icn-trash" />
									</RouteLink>
								) : null}
								{certDetails ? (
									<div className="certificate-details">
										<h4>Certificate Details</h4>
										<dl>
											<dt>Subject</dt>
											<dd>{certDetails.subject}</dd>
											<dt>Issuer</dt>
											<dd>{certDetails.issuer}</dd>
											<dt>Valid From</dt>
											<dd>{formatDate(certDetails.not_before)}</dd>
											<dt>Valid Until</dt>
											<dd>{formatDate(certDetails.not_after)}</dd>
											<dt>Serial Number</dt>
											<dd>{certDetails.serial_number}</dd>
											<dt>DNS Names</dt>
											<dd>{certDetails.dns_names.join(', ')}</dd>
										</dl>
									</div>
								) : null}
							</li>
						);
					}, this)}
				</ul>

				<RouteLink path={getAppPath("/routes/new")}>
					<button className="add-route-btn" onClick={this.__handleAddRouteBtnClick}>Add new domain</button>
				</RouteLink>
			</section>
		);
	},

	getInitialState: function () {
		return getState(this.props);
	},

	componentDidMount: function () {
		AppRoutesStore.addChangeListener(this.state.appStoreId, this.__handleStoreChange);
	},

	componentWillReceiveProps: function (nextProps) {
		var prevAppRoutesStoreId = this.state.appStoreId;
		var nextAppRoutesStoreId = getAppRoutesStoreId(nextProps);
		if ( !assertEqual(prevAppRoutesStoreId, nextAppRoutesStoreId) ) {
			AppRoutesStore.removeChangeListener(prevAppRoutesStoreId, this.__handleStoreChange);
			AppRoutesStore.addChangeListener(nextAppRoutesStoreId, this.__handleStoreChange);
			this.__handleStoreChange(nextProps);
		}
	},

	componentWillUnmount: function () {
		AppRoutesStore.removeChangeListener(this.state.appStoreId, this.__handleStoreChange);
	},

	__handleStoreChange: function (props) {
		this.setState(getState(props || this.props));
	}
});

export default AppRoutes;
