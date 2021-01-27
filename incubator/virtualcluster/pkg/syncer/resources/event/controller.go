/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package event

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	vcclient "sigs.k8s.io/multi-tenancy/incubator/virtualcluster/pkg/client/clientset/versioned"
	vcinformers "sigs.k8s.io/multi-tenancy/incubator/virtualcluster/pkg/client/informers/externalversions/tenancy/v1alpha1"
	"sigs.k8s.io/multi-tenancy/incubator/virtualcluster/pkg/syncer/apis/config"
	"sigs.k8s.io/multi-tenancy/incubator/virtualcluster/pkg/syncer/constants"
	"sigs.k8s.io/multi-tenancy/incubator/virtualcluster/pkg/syncer/manager"
	uw "sigs.k8s.io/multi-tenancy/incubator/virtualcluster/pkg/syncer/uwcontroller"
	"sigs.k8s.io/multi-tenancy/incubator/virtualcluster/pkg/util/listener"
	mc "sigs.k8s.io/multi-tenancy/incubator/virtualcluster/pkg/util/mccontroller"
	"sigs.k8s.io/multi-tenancy/incubator/virtualcluster/pkg/util/reconciler"
)

type controller struct {
	// super master event client (not used for now)
	client v1core.EventsGetter
	// super master event informer/lister/synced functions
	informer    coreinformers.Interface
	eventLister listersv1.EventLister
	eventSynced cache.InformerSynced
	nsLister    listersv1.NamespaceLister
	nsSynced    cache.InformerSynced

	// Connect to all tenant master event informers
	multiClusterEventController *mc.MultiClusterController
	// UWcontroller
	upwardEventController *uw.UpwardController

	acceptedEventObj map[string]runtime.Object
}

func NewEventController(config *config.SyncerConfiguration,
	client clientset.Interface,
	informer informers.SharedInformerFactory,
	vcClient vcclient.Interface,
	vcInformer vcinformers.VirtualClusterInformer,
	options manager.ResourceSyncerOptions) (manager.ResourceSyncer, *mc.MultiClusterController, *uw.UpwardController, error) {

	c := &controller{
		client:   client.CoreV1(),
		informer: informer.Core().V1(),
		acceptedEventObj: map[string]runtime.Object{
			"Pod":     &v1.Pod{},
			"Service": &v1.Service{},
		},
	}

	multiClusterEventController, err := mc.NewMCController(&v1.Event{}, c,
		mc.WithMaxConcurrentReconciles(constants.DwsControllerWorkerLow), mc.WithOptions(options.MCOptions))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create event mc controller: %v", err)
	}
	c.multiClusterEventController = multiClusterEventController

	c.nsLister = c.informer.Namespaces().Lister()
	c.eventLister = c.informer.Events().Lister()
	c.nsSynced = c.informer.Namespaces().Informer().HasSynced
	c.eventSynced = c.informer.Events().Informer().HasSynced
	if options.IsFake {
		c.nsSynced = func() bool { return true }
		c.eventSynced = func() bool { return true }
	}

	upwardEventController, err := uw.NewUWController(&v1.Event{}, c,
		uw.WithMaxConcurrentReconciles(constants.UwsControllerWorkerLow), uw.WithOptions(options.UWOptions))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create event upward controller: %v", err)
	}
	c.upwardEventController = upwardEventController

	informer.Core().V1().Events().Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				switch t := obj.(type) {
				case *v1.Event:
					return c.assignAcceptedEvent(t)
				case cache.DeletedFinalStateUnknown:
					if e, ok := t.Obj.(*v1.Event); ok {
						return c.assignAcceptedEvent(e)
					}
					utilruntime.HandleError(fmt.Errorf("unable to convert object %v to *v1.Event", obj))
					return false
				default:
					utilruntime.HandleError(fmt.Errorf("unable to handle object in super master event controller: %v", obj))
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc: c.enqueueEvent,
			},
		})

	return c, multiClusterEventController, upwardEventController, nil
}

func (c *controller) assignAcceptedEvent(e *v1.Event) bool {
	_, accepted := c.acceptedEventObj[e.InvolvedObject.Kind]
	return accepted
}

func (c *controller) enqueueEvent(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get key for object %v: %v", obj, err))
		return
	}
	c.upwardEventController.AddToQueue(key)
}

func (c *controller) StartDWS(stopCh <-chan struct{}) error {
	return nil
}

func (c *controller) StartPatrol(stopCh <-chan struct{}) error {
	return nil
}

func (c *controller) PatrollerDo() {
}

func (c *controller) Reconcile(request reconciler.Request) (reconciler.Result, error) {
	return reconciler.Result{}, nil
}

func (c *controller) GetListener() listener.ClusterChangeListener {
	return listener.NewMCControllerListener(c.multiClusterEventController)
}
