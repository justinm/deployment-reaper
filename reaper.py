#!/usr/bin/env python3

import logging
import os
import signal
import sys
import threading

import click
import typing

from datetime import datetime
from kubernetes import client, config
from util import label_selector, parse_time, parse_duration, time_to_str
from logging import getLogger

logger = getLogger(__name__)

logger.addHandler(logging.StreamHandler(stream=sys.stdout))

@click.command()
@click.pass_context
@click.option('--dryrun', '-n', is_flag=True, help="Do not restart, only log events")
@click.option('--verbose', '-v', count=True, help="Change the verbosity of the logs")
@click.option('--interval', '-i', required=True, default='60s', envvar='DEPLOYMENT_REAPER_INTERVAL', help='How often a reaping cycle should occur')
@click.option('--backoff-period', required=True, default='5m', envvar='DEPLOYMENT_REAPER_BACKOFF_PERIOD', help='The duration between the time a deployment is restarted and allowed to be restarted again')
@click.option('--default-max-age', required=True, envvar='DEPLOYMENT_REAPER_DEFAULT_MAX_AGE', help='The default maximum age of a container if no max-age label is provided')
def reaper(context, dryrun, verbose, interval, backoff_period, default_max_age):
    context.obj = {
        'dryrun': dryrun,
        'verbosity': verbose,
        'interval': interval,
        'backoff_period': backoff_period,
        'default_max_age': default_max_age,
        'managed_label': os.getenv('', 'reaper.kubernetes.io/managed'),
        'max_age_label': os.getenv('', 'reaper.kubernetes.io/max-age'),
        'restart_label': os.getenv('', 'reaper.kubernetes.io/restarted-on'),
    }

    logger.setLevel(30 - (verbose * 10))

    config.load_kube_config()

    cycle(context)

    stopped = threading.Event()

    def loop():
        while not stopped.wait(parse_duration(interval).seconds):
            cycle(context)

    signal.signal(signal.SIGINT, lambda sig, frame: stopped.set())
    runner = threading.Thread(target=loop)
    runner.daemon = True
    runner.start()

    runner.join()


def cycle(ctx):
    logger.debug('scanning for managed objects')
    managed_objects = get_managed_objects(ctx)

    logger.info('will process %d found objects' % (len(managed_objects),))

    for managed_object in managed_objects:
        if object_aged(ctx, managed_object):
            if not should_backoff(ctx, managed_object):
                if ctx.obj['dryrun']:
                    logger.info('would restart object %s/%s' % (managed_object.metadata.namespace, managed_object.metadata.name))
                else:
                    logger.info('restarting object %s/%s' % (managed_object.metadata.namespace, managed_object.metadata.name))
                    restart_managed_object(ctx, managed_object)
            else:
                logger.debug('backing off restart of object %s/%s' % (managed_object.metadata.namespace, managed_object.metadata.name))
        else:
            logger.debug('object %s/%s has not aged enough' % (managed_object.metadata.namespace, managed_object.metadata.name))


def should_backoff(ctx, managed_object: typing.Union[client.V1Deployment, client.V1DaemonSet]) -> bool:
    if ctx.obj['restart_label'] in managed_object.metadata.annotations.keys():
        last_restarted = parse_time(managed_object.metadata.annotations[ctx.obj['restart_label']])
        backoff_period = parse_duration(ctx.obj['backoff_period'])

        if datetime.utcnow() < last_restarted + backoff_period:
            return True

    return False


def get_managed_objects(ctx) -> [typing.Union[client.V1Deployment, client.V1DaemonSet]]:
    apps = client.AppsV1Api()

    selector = label_selector({
        ctx.obj['managed_label']: True
    })

    deployments = apps.list_deployment_for_all_namespaces(label_selector=selector, watch=False)
    daemonsets = apps.list_daemon_set_for_all_namespaces(label_selector=selector, watch=False)

    return deployments.items + daemonsets.items


def objects_max_age(ctx, obj: typing.Union[client.V1Deployment, client.V1DaemonSet]):
    if ctx.obj['max_age_label'] in obj.metadata.labels:
        return parse_duration(obj.metadata.labels[ctx.obj['max_age_label']])

    return parse_duration(ctx.obj['default_max_age'])


def object_aged(ctx, obj: typing.Union[client.V1Deployment, client.V1DaemonSet]) -> bool:
    max_age = objects_max_age(ctx, obj)
    core = client.CoreV1Api()
    pods = core.list_namespaced_pod(namespace=obj.metadata.namespace, label_selector=label_selector(obj.spec.selector.match_labels))

    for pod in pods.items:
        if pod.status.phase == 'Running':
            if get_pod_runtime(pod) > max_age:
                return True

    return False


def get_pod_runtime(pod: client.V1Pod):
    return datetime.utcnow() - pod.metadata.creation_timestamp.replace(tzinfo=None)


def restart_managed_object(ctx, obj: typing.Union[client.V1Deployment, client.V1DaemonSet]):
    restarted_on = time_to_str(datetime.utcnow())

    obj.spec.template.metadata.annotations[ctx.obj['restart_label']] = restarted_on

    apps = client.AppsV1Api()

    if isinstance(obj, (client.V1Deployment,)):
        apps.patch_namespaced_deployment(
            name=obj.metadata.name,
            namespace=obj.metadata.namespace,
            body=obj,
        )
    elif isinstance(obj, (client.V1DaemonSet,)):
        apps.patch_namespaced_daemon_set(
            name=obj.metadata.name,
            namespace=obj.metadata.namespace,
            body=obj,
        )


if __name__ == '__main__':
    reaper()
