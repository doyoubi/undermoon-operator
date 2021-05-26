package pkg

import (
	"github.com/google/uuid"
	"math/rand"

	"github.com/rs/zerolog/log"
	"gonum.org/v1/gonum/stat/distuv"
)

func chooseSlotFromNormDist(d distuv.Normal) int {
	s := d.Sigma
	// 4 * s contains about 95% of the distribution
	slot := int((d.Rand() + 2*s) * slotNumber / (4 * s))
	if slot < 0 {
		slot = 0
	} else if slot >= slotNumber {
		slot = slotNumber - 1
	}
	return slot
}

func randomGetKey(m map[string]string) (string, string) {
	r := rand.Int() % len(m)
	i := 0
	for k, v := range m {
		if i == r {
			return k, v
		}
		i++
	}

	const msg = "should be able to get a key"
	log.Panic().Msg(msg)
	panic(msg)
}

func nil2Empty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func getKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}

	ss := make([]string, 0, len(m))
	for k := range m {
		ss = append(ss, k)
	}
	return ss
}

type slotTags struct {
	tags [slotNumber]string
}

func newSlotTags() *slotTags {
	tags := [slotNumber]string{}
	n := 0
	for n != slotNumber {
		tag := uuid.NewString()[:6]
		slot := Slot(tag)
		if len(tags[slot]) != 0 {
			continue
		}

		tags[slot] = tag
		n++
	}
	return &slotTags{tags: tags}
}

func (st *slotTags) getTag(slot int) string {
	return st.tags[slot]
}
