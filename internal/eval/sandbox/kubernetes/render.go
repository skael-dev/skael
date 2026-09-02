package kubernetes

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

const (
	proxyPort     = 8888
	ownerLabelKey = "whetstone.owner"
	pidLabelKey   = "whetstone.owner.pid"
	roleLabelKey  = "whetstone.role"
	// idleSeconds keeps the pod alive so the driver can stage the workspace
	// before argv starts. The sleep is bounded by ActiveDeadlineSeconds.
	idleSeconds = "86400"
)

// podStagingMargin is added to the run's own timeout when setting
// ActiveDeadlineSeconds. Kubelet starts charging that deadline at pod
// creation, before scheduling, image pull and workspace staging even begin —
// none of which count against runCtx, whose clock starts only once the
// session is running. Without a margin, a slow image pull ate into the
// run's own budget from the outside, so the pod could be killed before
// runCtx expired: an exec error rather than the run's own TimedOut result.
const podStagingMargin = 5 * time.Minute

// names is one session's resource name set. Every resource shares the random
// suffix so a sweep can group them.
type names struct {
	Session   string
	Proxy     string
	ConfigMap string
	Policy    string
	owner     string
}

func newNames() (names, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return names{}, fmt.Errorf("kubernetes: naming a session: %w", err)
	}
	id := hex.EncodeToString(b[:])
	return names{
		Session:   "whetstone-run-" + id,
		Proxy:     "whetstone-proxy-" + id,
		ConfigMap: "whetstone-proxy-" + id,
		Policy:    "whetstone-egress-" + id,
		owner:     id,
	}, nil
}

func (n names) labels(role string) map[string]string {
	return map[string]string{
		ownerLabelKey: n.owner,
		pidLabelKey:   fmt.Sprint(os.Getpid()),
		roleLabelKey:  role,
	}
}

func boolPtr(b bool) *bool    { return &b }
func i64Ptr(i int64) *int64   { return &i }
func strPtr(s string) *string { return &s }

// SessionPod renders the pod one session runs in. Its entrypoint idles: argv
// runs through exec, after the workspace has been staged.
func SessionPod(rs sandbox.RunSpec, o Options, n names) (*corev1.Pod, error) {
	if err := rs.Validate(); err != nil {
		return nil, err
	}
	if len(rs.Mounts) > 0 {
		return nil, fmt.Errorf("kubernetes: run declares %d host mounts, and a pod has no host to mount from. Supply agent credentials as environment variables instead (CLAUDE_CODE_OAUTH_TOKEN)", len(rs.Mounts))
	}
	if err := o.CheckNetwork(rs.Network); err != nil {
		return nil, err
	}

	workdir := rs.WorkDir
	if workdir == "" {
		workdir = sandbox.DefaultWorkDir
	}

	env := make([]corev1.EnvVar, 0, len(rs.Env)+4)
	for _, e := range rs.Env {
		k, v, _ := strings.Cut(e, "=")
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}
	if rs.Network == sandbox.NetAllowlist {
		proxy := fmt.Sprintf("http://%s:%d", n.Proxy, proxyPort)
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env = append(env, corev1.EnvVar{Name: k, Value: proxy})
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: n.Session, Namespace: o.Namespace, Labels: n.labels("session")},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:        i64Ptr(int64((rs.Timeout + podStagingMargin).Seconds())),
			AutomountServiceAccountToken: boolPtr(false),
			Containers: []corev1.Container{{
				Name:         "session",
				Image:        rs.Image.Tag,
				Command:      []string{"sleep"},
				Args:         []string{idleSeconds},
				WorkingDir:   workdir,
				Env:          env,
				VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: workdir}},
				Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(o.CPUs),
					corev1.ResourceMemory: resource.MustParse(o.Memory),
				}},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: boolPtr(false),
					RunAsUser:                i64Ptr(1000),
					RunAsNonRoot:             boolPtr(true),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
			Volumes: []corev1.Volume{{
				Name:         "workspace",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			}},
		},
	}
	applySharedPodSettings(&pod.Spec, o)
	return pod, nil
}

// ProxyPod renders the per-session tinyproxy. It is a separate pod, not a
// sidecar: containers in one pod share an IP, so a policy strict enough to
// force the session through the proxy would also cut the proxy off.
func ProxyPod(o Options, n names) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: n.Proxy, Namespace: o.Namespace, Labels: n.labels("proxy")},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: boolPtr(false),
			Containers: []corev1.Container{{
				Name:         "proxy",
				Image:        o.Image,
				Command:      []string{"tinyproxy", "-d", "-c", "/etc/tinyproxy/tinyproxy.conf"},
				Ports:        []corev1.ContainerPort{{ContainerPort: proxyPort}},
				VolumeMounts: []corev1.VolumeMount{{Name: "config", MountPath: "/etc/tinyproxy"}},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: boolPtr(false),
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
						Add:  []corev1.Capability{"SETUID", "SETGID"},
					},
				},
				ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
					TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(proxyPort)},
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "config",
				VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: n.ConfigMap},
				}},
			}},
		},
	}
	applySharedPodSettings(&pod.Spec, o)
	return pod
}

func applySharedPodSettings(s *corev1.PodSpec, o Options) {
	if o.RuntimeClass != "" {
		s.RuntimeClassName = strPtr(o.RuntimeClass)
	}
	if o.PullSecret != "" {
		s.ImagePullSecrets = []corev1.LocalObjectReference{{Name: o.PullSecret}}
	}
}

// ProxyConfigMap splits imagespec's rendered proxy configuration into the two
// files tinyproxy expects. The Docker driver delivers the same two bytes by
// "docker cp"; a ConfigMap is the same payload by a different transport.
func ProxyConfigMap(allow []string, o Options, n names) (*corev1.ConfigMap, error) {
	cfg, err := imagespec.ProxyConfig(allow)
	if err != nil {
		return nil, err
	}
	conf, filter, found := strings.Cut(cfg, "\n"+imagespec.FilterMarker+"\n")
	if !found {
		return nil, fmt.Errorf("kubernetes: rendered proxy config has no %q marker", imagespec.FilterMarker)
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: n.ConfigMap, Namespace: o.Namespace, Labels: n.labels("proxy")},
		Data:       map[string]string{"tinyproxy.conf": conf, "filter": filter},
	}, nil
}

// EgressPolicy confines the session pod to the proxy and DNS. This is the
// only real enforcement; the proxy environment variables merely tell the
// workload where to go.
func EgressPolicy(o Options, n names) *networkingv1.NetworkPolicy {
	dnsUDP := corev1.ProtocolUDP
	dnsTCP := corev1.ProtocolTCP
	dnsPort := intstr.FromInt(53)
	proxy := intstr.FromInt(proxyPort)
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: n.Policy, Namespace: o.Namespace, Labels: n.labels("session")},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				ownerLabelKey: n.owner, roleLabelKey: "session",
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{ownerLabelKey: n.owner, roleLabelKey: "proxy"},
					}}},
					Ports: []networkingv1.NetworkPolicyPort{{Port: &proxy}},
				},
				{
					// A DNS rule with no To peer would allow port 53 to any
					// destination, in the cluster and out. That is an
					// exfiltration channel over DNS, which internal/gate treats
					// as unappealable. Scope it to CoreDNS by the labels every
					// distribution ships it under.
					To: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
						},
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"k8s-app": "kube-dns"},
						},
					}},
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &dnsUDP, Port: &dnsPort},
						{Protocol: &dnsTCP, Port: &dnsPort},
					},
				},
			},
		},
	}
}

// DenyAllEgressPolicy is NetNone: no egress rule at all.
func DenyAllEgressPolicy(o Options, n names) *networkingv1.NetworkPolicy {
	p := EgressPolicy(o, n)
	p.Spec.Egress = nil
	return p
}
